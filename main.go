// Copyright (C) 2014 Ian Bishop
//
// This program is free software; you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation; either version 2 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License along
// with this program; if not, write to the Free Software Foundation, Inc.,
// 51 Franklin Street, Fifth Floor, Boston, MA 02110-1301 USA.

// Package hamsdr implements a software-defined radio scanner
//
// hamsdr requires rtlsdr library
//
package main

import (
	"bufio"
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"time"

	rtl "github.com/jpoirier/gortlsdr"
)

var dongleTimer time.Time

const (
	defaultSampleRate = 24000
	defaultBufLen     = (1 * 16384)
	maximumOversample = 16
	maximumBufLen     = (maximumOversample * defaultBufLen)
	autoGain          = -100
	bufferDump        = 4096

	//cicTableMax = 10

	frequenciesLimit = 1000
)

// used to parse multiple -f params
type frequencies []uint32
type exitChan chan struct{}

type dongleState struct {
	dev            *rtl.Context
	devIndex       int
	freq           uint32
	rate           uint32
	gain           int
	ppmError       int
	offsetTuning   bool
	directSampling int
	mute           int
	demodTarget    *demodState
	lpChan         chan []int16
}

type demodState struct {
	lowpassed []int16
	lpIHist   [10][6]int16
	lpQHist   [10][6]int16
	result    []int16 // ?
	//resultLen  int                  // ?
	droopIHist [9]int16
	droopQHist [9]int16
	rateIn     int
	rateOut    int
	//rateOut2           int
	nowR               int
	nowJ               int
	preR               int
	preJ               int
	prevIndex          int
	downsample         int // min 1, max 256
	postDownsample     int
	outputScale        int
	squelchLevel       int
	conseqSquelch      int
	squelchHits        int
	terminateOnSquelch int
	downsamplePasses   int
	compFirSize        int
	customAtan         int
	deemph, deemphA    int
	nowLpr             int
	prevLprIndex       int
	dcBlock, dcAvg     int
	modeDemod          func(fm *demodState)
}

type outputState struct {
	file     *os.File
	filename string
	rate     int

	resultChan chan []int16
}

type controllerState struct {
	freqs   frequencies
	freqNow int
	edge    int

	hopChan chan bool
}

var dongle *dongleState
var demod *demodState
var output *outputState
var controller *controllerState

var actualBufLen int
var lcmPost = [17]int{1, 1, 1, 3, 1, 5, 3, 7, 1, 9, 5, 11, 3, 13, 7, 15, 1}

func init() {
	dongle = &dongleState{}
	output = &outputState{}
	demod = &demodState{}
	controller = &controllerState{}

	dongle.rate = defaultSampleRate
	dongle.gain = autoGain // tenths of a dB
	dongle.demodTarget = demod
	dongle.lpChan = make(chan []int16, 1)

	demod.rateIn = defaultSampleRate
	demod.rateOut = defaultSampleRate
	demod.conseqSquelch = 10
	demod.squelchHits = 11
	demod.postDownsample = 1 // once this works, default = 4

	output.rate = defaultSampleRate
	output.resultChan = make(chan []int16, 1)

	controller.hopChan = make(chan bool)
}

func setFreqs(val string) (freqs frequencies, err error) {
	if val == "" {
		return
	}

	var freq uint32
	var start, stop, step uint32

	step = 25000

	bits := strings.Split(val, ":")

	switch len(bits) {
	case 1:
		freq, err = freqHz(bits[0])
		if err != nil {
			return
		}
		freqs = append(freqs, freq)
		return
	case 3:
		step, err = freqHz(bits[2])
		if err != nil {
			return
		}
		fallthrough
	case 2:
		start, err = freqHz(bits[0])
		if err != nil {
			return
		}
		stop, err = freqHz(bits[1])
		if err != nil {
			return
		}
	default:
		err = fmt.Errorf("Frequency range could not be parsed")
		return
	}

	for j := start; j <= stop; j += step {
		if len(freqs) > frequenciesLimit {
			break
		}
		freqs = append(freqs, j)
	}

	return
}

// Convert frequency string to Hz
// 90.2M = 90200000
// 25K = 25000
func freqHz(freqStr string) (freq uint32, err error) {
	var u64 uint64
	upper := strings.ToUpper(freqStr)

	switch {
	case strings.HasSuffix(upper, "K"):
		upper = strings.TrimSuffix(upper, "K")
		u64, err = strconv.ParseUint(upper, 10, 32)
		freq = uint32(u64 * 1e3)
	case strings.HasSuffix(upper, "M"):
		upper = strings.TrimSuffix(upper, "M")
		u64, err = strconv.ParseUint(upper, 10, 32)
		freq = uint32(u64 * 1e6)
	default:
		if last := len(upper) - 1; last >= 0 {
			upper = upper[:last]
		}
		u64, err = strconv.ParseUint(upper, 10, 32)
		freq = uint32(u64)
	}
	return
}

func rtlsdrCallback(buf []byte, ctx *rtl.UserCtx) {
	var i int

	if dongle.mute > 0 && dongle.mute < len(buf) {
		for i = 0; i < dongle.mute; i++ {
			buf[i] = 127
		}
		dongle.mute = 0
	}
	if !dongle.offsetTuning {
		rotate90(buf)
	}
	buf16 := make([]int16, len(buf))
	for i := range buf {
		buf16[i] = int16(buf[i]) - 127
	}
	//fmt.Fprintf(os.Stderr, "buf %x %x %x %x, buf16 %x %x %x %x, buf len %d\n", buf[0], buf[1], buf[2], buf[3], uint16(buf16[0]), uint16(buf16[1]), uint16(buf16[2]), uint16(buf16[3]), len(buf))

	dongle.lpChan <- buf16
}

// ReadAsync blocks until CancelAsync
func dongleRoutine(wg *sync.WaitGroup) {
	defer wg.Done()
	err := dongle.dev.ReadAsync(rtlsdrCallback, nil, rtl.DefaultAsyncBufNumber, rtl.DefaultBufLength)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ReadAsync failed, err %s\n", err)
	}

	close(dongle.lpChan)

	fmt.Fprintf(os.Stderr, "Returning from dongleRoutine\n")
}

func demodRoutine(wg *sync.WaitGroup) {
	var ok bool

	defer wg.Done()

	for {
		demod.lowpassed, ok = <-dongle.lpChan

		if !ok {
			close(output.resultChan)
			close(controller.hopChan)
			fmt.Fprintf(os.Stderr, "Returning from demodRoutine\n")
			return
		}

		//fmt.Fprintf(os.Stderr, "demod, lp %x %x, len %d\n", lp[0], lp[1], len(lp))
		demod.fullDemod()

		if demod.squelchLevel > 0 && demod.squelchHits > demod.conseqSquelch {
			// hair trigger
			demod.squelchHits = demod.conseqSquelch + 1
			controller.hopChan <- true
			continue
		}
		//fmt.Fprintf(os.Stderr, "demod, result %x %x, len %d\n", d.result[0], d.result[1], len(d.result))
		//memcpy(o->result, d->result, 2*d->result_len);
		var result []int16
		result = append(result, demod.result...)
		output.resultChan <- result
	}
}

// thoughts for multiple dongles
// might be no good using a controller thread if retune/rate blocks
func controllerRoutine(wg *sync.WaitGroup) {
	var err error

	defer wg.Done()

	s := controller

	// set up primary channel
	optimalSettings(int(s.freqs[0]), demod.rateIn)

	// Set the frequency
	err = dongle.dev.SetCenterFreq(int(dongle.freq))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error setting frequency %d\n", dongle.freq)
		return
	}
	fmt.Fprintf(os.Stderr, "Oversampling input by: %dx.\n", demod.downsample)
	fmt.Fprintf(os.Stderr, "Oversampling output by: %dx.\n", demod.postDownsample)
	fmt.Fprintf(os.Stderr, "Buffer size: %0.2fms\n", 1000*0.5*float32(actualBufLen)/float32(dongle.rate))

	// Set the sample rate
	err = dongle.dev.SetSampleRate(int(dongle.rate))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error setting sample rate %d\n", dongle.rate)
		return
	}
	fmt.Fprintf(os.Stderr, "Output at %d Hz.\n", demod.rateIn/demod.postDownsample)

	for {
		_, ok := <-controller.hopChan
		if !ok {
			fmt.Fprintf(os.Stderr, "Returning from controllerRoutine\n")
			return
		}

		if len(s.freqs) <= 1 {
			continue
		}
		// hacky hopping
		s.freqNow = (s.freqNow + 1) % len(s.freqs)
		//fmt.Fprintf(os.Stderr, "controller, freqnow %d\n", s.freqs[s.freqNow])
		optimalSettings(int(s.freqs[s.freqNow]), demod.rateIn)
		err = dongle.dev.SetCenterFreq(int(dongle.freq))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error setting frequency %d\n", dongle.freq)
			return
		}
		dongle.mute = bufferDump
	}
}

func outputRoutine(wg *sync.WaitGroup) {
	var err error

	defer wg.Done()
	f := bufio.NewWriter(output.file)
	defer f.Flush()
	for {
		result, ok := <-output.resultChan
		if !ok {
			fmt.Fprintf(os.Stderr, "Returning from outputRoutine\n")
			return
		}

		err = binary.Write(f, binary.LittleEndian, result)
		if err != nil {
			fmt.Fprintf(os.Stderr, "output write error: %s\n", err)
		}
	}
}

func (d *demodState) fullDemod() {
	var i int

	lowPass(d)

	// power squelch
	if d.squelchLevel > 0 {
		sr := rms(d.lowpassed, 1)
		if sr < d.squelchLevel {
			d.squelchHits++
			for i = 0; i < len(d.lowpassed); i++ {
				d.lowpassed[i] = 0
			}
		} else {
			d.squelchHits = 0
		}
	}

	d.modeDemod(d)
}

func (f *frequencies) String() string {
	return fmt.Sprintf("%d", *f)
}

func (f *frequencies) Set(val string) error {
	freqs, err := setFreqs(val)
	if err != nil {
		return err
	}

	*f = append(*f, freqs...)

	return nil
}

func main() {
	var err error

	flag.IntVar(&dongle.devIndex, "d", 0, "dongle device index")
	//freqStr := flag.String("f", "", "frequency or range of frequencies, and step e.g 92900:100100:25000")
	flag.Var(&controller.freqs, "f", "frequency or range of frequencies, and step e.g 92900:100100:25000")
	flag.IntVar(&demod.squelchLevel, "l", 0, "squelch level")
	rateIn := flag.Int("s", 0, "sample rate")
	flag.IntVar(&dongle.ppmError, "p", 0, "ppm error")
	demodMode := flag.String("M", "am", "demodulation mode [fm, am]")

	flag.Parse()

	/*
		controller.freqs, err = setFreqs(*freqStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to parse frequencies %s\n", err)
			return
		}
	*/

	switch *demodMode {
	case "fm":
		demod.modeDemod = fmDemod
	default:
		demod.modeDemod = amDemod
	}

	if *rateIn > 0 {
		demod.rateIn = *rateIn
	}

	demod.rateOut = demod.rateIn

	if len(controller.freqs) == 0 {
		controller.freqs = append(controller.freqs, 100000000)
	}

	/*
		if len(controller.freqs) == 0 {
			fmt.Fprintln(os.Stderr, "Please specify a frequency.")
			return
		}
	*/

	if len(controller.freqs) >= frequenciesLimit {
		fmt.Fprintf(os.Stderr, "Too many channels, maximum %d.\n", frequenciesLimit)
		return
	}

	if len(controller.freqs) > 1 && demod.squelchLevel == 0 {
		fmt.Fprintln(os.Stderr, "Please specify a squelch level.  Required for scanning multiple frequencies.")
		return
	}

	// quadruple sample_rate to limit to Δθ to ±π/2
	demod.rateIn *= demod.postDownsample

	if output.rate == 0 {
		output.rate = demod.rateOut
	}

	if len(controller.freqs) > 1 {
		demod.terminateOnSquelch = 0
	}

	if flag.Arg(0) != "" {
		output.filename = flag.Arg(0)
	} else {
		output.filename = "-"
	}

	actualBufLen = lcmPost[demod.postDownsample] * defaultBufLen

	dongle.dev, err = rtl.Open(dongle.devIndex)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open dongle, '%s', exiting\n", err)
		return
	}
	defer dongle.dev.Close()

	// Set the tuner gain
	if dongle.gain == autoGain {
		fmt.Fprintf(os.Stderr, "Setting auto gain\n")
		err = dongle.dev.SetTunerGainMode(false)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error setting tuner auto-gain: %s\n", err)
			return
		}
	} else {
		dongle.gain, err = nearestGain(dongle.dev, dongle.gain)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting nearest gain to %d: %s\n", dongle.gain, err)
			return
		}
		err = dongle.dev.SetTunerGain(dongle.gain)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error setting tuner manual gain to %d: %s\n", dongle.gain)
			return
		}
	}

	if dongle.ppmError > 0 {
		err = dongle.dev.SetFreqCorrection(dongle.ppmError)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error setting frequency correction to %d: %s\n", dongle.ppmError, err)
			return
		}
	}

	if output.filename == "-" {
		output.file = os.Stdout
	} else {
		output.file, err = os.OpenFile(output.filename, os.O_RDWR|os.O_APPEND, 0660)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return
		}
		defer output.file.Close()
	}

	// Reset endpoint before we start reading from it (mandatory)
	err = dongle.dev.ResetBuffer()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}

	signalChan := make(chan os.Signal, 1)
	quit := make(exitChan)
	signal.Notify(signalChan, os.Interrupt)
	go func() {
		for _ = range signalChan {
			fmt.Fprintln(os.Stderr, "\nReceived an interrupt, stopping services...")
			close(quit)
			return
		}
	}()
	var wg sync.WaitGroup

	wg.Add(4)

	go controllerRoutine(&wg)
	go outputRoutine(&wg)
	go demodRoutine(&wg)
	go dongleRoutine(&wg)

	controller.hopChan <- true

	<-quit
	fmt.Fprintf(os.Stderr, "rtlsdr CancelAsync()\n")
	if err := dongle.dev.CancelAsync(); err != nil {
		fmt.Fprintf(os.Stderr, "Error canceling async %s\n", err)
	}

	fmt.Fprintf(os.Stderr, "Waiting for goroutines to finish...\n")
	wg.Wait()

	fmt.Fprintf(os.Stderr, "Exiting...\n")
}
