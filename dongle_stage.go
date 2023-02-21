package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	rtl "github.com/jpoirier/gortlsdr"
)

var (
	errNoDevicesAvailable = errors.New("no rtlsdr devices connected")
)

func newDongleStage(ctx context.Context, cfg *config) (*dongleState, error) {
	c, can := context.WithCancel(ctx)
	return &dongleState{
		ctx: c, cancel: can, devSerial: cfg.Scanner.DongleSerial,
		paused: false,
		rate:   defaultSampleRate, gain: cfg.Params.Gain,
		preRotate: true, ppmError: cfg.Params.PPMError,
	}, nil
}

func (d *dongleState) startDevice() error {
	dev, err := openDongle(d.devSerial)
	if err != nil {
		return err
	}

	d.dev = dev
	return nil
}

type dongleState struct {
	ctx            context.Context
	cancel         context.CancelFunc
	dev            managedDongle
	devSerial      string
	freq           uint32
	rate           uint32
	gain           int
	ppmError       int
	offsetTuning   bool
	directSampling int
	mute           int
	preRotate      bool
	paused         bool
}

func (d *dongleState) stop() {
	d.cancel()
}

func (d *dongleState) pause() {
	d.paused = true
}

func (d *dongleState) resume() {
	d.paused = false
}

func (d *dongleState) routine(wg *sync.WaitGroup, toDemod chan []int16) (func(), error) {
	rtlsdrCallback := func(buf []byte) {
		if d.paused {
			return
		}

		if d.mute > 0 && d.mute < len(buf) {
			for i := 0; i < d.mute; i++ {
				buf[i] = 127
			}
			d.mute = 0
		}

		if d.preRotate {
			rotate90(buf)
		}

		buf16 := make([]int16, len(buf))
		for i := range buf {
			buf16[i] = int16(buf[i]) - 127
		}

		toDemod <- buf16
	}

	return func() {
		go func() {
			<-d.ctx.Done()
			d.dev.Close()
		}()

		defer wg.Done()
		err := d.dev.ReadAsync(rtlsdrCallback, nil, 0, 0)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ReadAsync failed, err %s\n", err)
		}

		fmt.Fprintf(os.Stderr, "Returning from dongleRoutine\n")
	}, nil
}

type managedDongle struct {
	*rtl.Context
}

func openDongle(dongleSerial string) (managedDongle, error) {
	if rtl.GetDeviceCount() == 0 {
		return managedDongle{}, errNoDevicesAvailable
	}

	fmt.Fprintf(os.Stderr, "have %d available devices\n", rtl.GetDeviceCount())
	fmt.Fprintln(os.Stderr, dongleSerial)

	var (
		err    error = nil
		devIdx int   = 0
	)

	if dongleSerial != "" {
		if devIdx, err = rtl.GetIndexBySerial(dongleSerial); err != nil {
			return managedDongle{}, err
		}
	}

	dev, err := rtl.Open(devIdx)
	return managedDongle{dev}, err
}

func (md *managedDongle) close() {
	fmt.Fprintln(os.Stderr, "[dongle] closing connection to device")

	if err := md.CancelAsync(); err != nil {
		fmt.Fprintf(os.Stderr, "[dongle] Error cancelling async: '%s'\n", err)
	}

	if err := md.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "[dongle] Error closing device: '%s'\n", err)
	}

	fmt.Fprintln(os.Stderr, "[dongle] closed connection with device")
}
