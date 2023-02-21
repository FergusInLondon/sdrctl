package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"reflect"
	"sync"
)

var (
	errSquelchLevelRequired = errors.New("Squelch level required when scanning a frequency range.")
)

type controllerState struct {
	cfg         *config
	ctx         context.Context
	cancel      context.CancelFunc
	dongleStage *dongleState
	demodStage  *demodState
	outputStage *outputState
	hopChan     chan struct{}
	freqs       []uint32
	freqNow     int
	wbMode      bool
}

func newSDRController(
	ctx context.Context, cfg *config,
	dongle *dongleState,
	demod *demodState,
	output *outputState,
) (*controllerState, error) {
	freqs, err := cfg.listenFreqs()
	if len(freqs) > 1 && cfg.Params.Squelch == 0 {
		return nil, errSquelchLevelRequired
	}

	stageCtx, cancel := context.WithCancel(ctx)
	return &controllerState{
		ctx: stageCtx, cancel: cancel, hopChan: make(chan struct{}),
		dongleStage: dongle, demodStage: demod, outputStage: output,
		freqs: freqs, cfg: cfg,
	}, err
}

func (c *controllerState) _pipeline(completed chan struct{}) error {
	var (
		err                   error
		wg                    sync.WaitGroup
		toDemod               = make(chan []int16, 1)
		toOutput              = make(chan []int16, 1)
		dongle, demod, output func()
	)

	if dongle, err = c.dongleStage.routine(&wg, toDemod); err != nil {
		return err
	}

	if demod, err = c.demodStage.routine(&wg, toDemod, toOutput, c.hopChan); err != nil {
		return err
	}

	if output, err = c.outputStage.routine(&wg, toOutput); err != nil {
		return err
	}

	wg.Add(3)
	go output()
	go demod()
	go dongle()
	wg.Wait()

	close(toDemod)
	close(toOutput)
	close(completed)
	return nil
}

func (c *controllerState) _config_demod() {
	switch c.cfg.Params.DemodMode {
	case "wbfm":
		c.wbMode = true
		c.demodStage.customAtan = 1
		//demod.post_downsample = 4;
		c.demodStage.deemph = true
		c.demodStage.squelchLevel = 0
		c.demodStage.rateIn = 170000
		c.demodStage.rateOut = 170000
		c.demodStage.rateOut2 = 32000
		c.outputStage.rate = 32000
		fallthrough
	case "fm":
		c.demodStage.modeDemod = fmDemod
	default:
		c.demodStage.modeDemod = amDemod
	}

	// @note: we're just overwriting some of those values above...?

	// quadruple sample_rate to limit to Δθ to ±π/2
	c.demodStage.rateIn *= c.demodStage.postDownsample

	if c.outputStage.rate == 0 {
		c.outputStage.rate = c.demodStage.rateOut
	}

	if c.demodStage.deemph {
		c.demodStage.deemphA = int(
			//round(1.0/(1.0-math.Exp(-1.0/(float64(demod.rateOut)*75e-6))), 0),
			round(1.0 / (1.0 - math.Exp(-1.0/(float64(c.demodStage.rateOut)*75e-6)))),
		)
		fmt.Fprintf(os.Stderr, "Deempha %d\n", c.demodStage.deemphA)
	}
}

func (c *controllerState) _config_dongle() error {
	if c.dongleStage.gain == autoGain {
		fmt.Fprintf(os.Stderr, "Setting auto gain\n")
		if err := c.dongleStage.dev.SetTunerGainMode(false); err != nil {
			fmt.Fprintf(os.Stderr, "Error setting tuner auto-gain: %s\n", err)
			return err
		}

	} else {
		c.dongleStage.gain *= 10
		nearest_gain, err := nearestGain(c.dongleStage.dev.Context, c.dongleStage.gain)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting nearest gain to %d: %s\n", nearest_gain, err)
			return err
		}

		c.dongleStage.gain = nearest_gain
		if err := c.dongleStage.dev.SetTunerGain(c.dongleStage.gain); err != nil {
			fmt.Fprintf(os.Stderr, "Error setting tuner manual gain to %d: %s\n", nearest_gain, err)
			return err
		}
	}

	fmt.Fprintf(os.Stderr, "Set tuner gain to %d.", c.dongleStage.gain)

	if c.dongleStage.ppmError > 0 {
		if err := c.dongleStage.dev.SetFreqCorrection(c.dongleStage.ppmError); err != nil {
			fmt.Fprintf(os.Stderr, "Error setting frequency correction to %d: %s\n", c.dongleStage.ppmError, err)
			return err
		}
	}

	fmt.Fprintf(os.Stderr, "Set error set to %d ppm.", c.dongleStage.ppmError)
	return c.dongleStage.dev.ResetBuffer()
}

func (c *controllerState) run() error {
	var (
		err              error
		pipelineFinished = make(chan struct{})
		lcmPost          = [17]int{1, 1, 1, 3, 1, 5, 3, 7, 1, 9, 5, 11, 3, 13, 7, 15, 1}
	)

	if err := c.dongleStage.startDevice(); err != nil {
		fmt.Printf("unable to open device: %s\n", err)
		return err
	}

	// Dependent Config
	c._config_demod()
	if err := c._config_dongle(); err != nil {
		fmt.Fprintf(os.Stderr, "[controller] failed to configure rtl device: %s", err)
		return err
	}

	if err := c._pipeline(pipelineFinished); err != nil {
		return err
	}

	if c.wbMode {
		for i := range c.freqs {
			c.freqs[i] += 16000
		}
	}

	// set up primary channel
	c.optimalSettings(int(c.freqs[0]))
	c.demodStage.squelchLevel = squelchToRms(c.demodStage.squelchLevel, c.dongleStage, c.demodStage)

	// Set the frequency
	if err := c.dongleStage.dev.SetCenterFreq(int(c.dongleStage.freq)); err != nil {
		fmt.Fprintf(os.Stderr, "Error setting frequency %d\n", c.dongleStage.freq)
		return err
	}

	actualBufLen := lcmPost[c.demodStage.postDownsample] * defaultBufLen
	fmt.Fprintf(os.Stderr, "Tuned to %d Hz\n", c.dongleStage.freq)
	fmt.Fprintf(os.Stderr, "Oversampling input by: %dx.\n", c.demodStage.downsample)
	fmt.Fprintf(os.Stderr, "Oversampling output by: %dx.\n", c.demodStage.postDownsample)
	fmt.Fprintf(os.Stderr, "Buffer size: %0.2fms\n", 1000*0.5*float32(actualBufLen)/float32(c.dongleStage.rate))

	// Set the sample rate
	if err := c.dongleStage.dev.SetSampleRate(int(c.dongleStage.rate)); err != nil {
		fmt.Fprintf(os.Stderr, "Error setting sample rate %d\n", c.dongleStage.rate)
		return err
	}

	fmt.Fprintf(os.Stderr, "Sampling at %d S/s.\n", c.dongleStage.rate)
	fmt.Fprintf(os.Stderr, "Output at %d Hz.\n", c.demodStage.rateIn/c.demodStage.postDownsample)

	for {
		select {
		case <-c.ctx.Done():
			fmt.Fprintf(os.Stderr, "Returning from controllerRoutine\n")
			return err
		case <-c.hopChan:
			if len(c.freqs) <= 1 {
				continue
			}
			c.freqNow = (c.freqNow + 1) % len(c.freqs)
			c.optimalSettings(int(c.freqs[c.freqNow]))
			if err := c.dongleStage.dev.SetCenterFreq(int(c.dongleStage.freq)); err != nil {
				fmt.Fprintf(os.Stderr, "Error setting frequency %d\n", c.dongleStage.freq)
				return err
			}
			c.dongleStage.mute = bufferDump
		}
	}

	c.dongleStage.stop()
	c.demodStage.stop()
	c.outputStage.stop()
	<-pipelineFinished
	return nil
}

func (c *controllerState) stop() {
	c.cancel()
}

func (c *controllerState) optimalSettings(freq int) {
	var captureFreq, captureRate int

	c.demodStage.downsample = (minimumRate / c.demodStage.rateIn) + 1
	captureFreq = freq
	captureRate = c.demodStage.downsample * c.demodStage.rateIn

	if c.dongleStage.preRotate {
		captureFreq = freq + captureRate/4
	}

	c.demodStage.outputScale = (1 << 15) / (128 * c.demodStage.downsample)
	fmt.Fprintf(os.Stderr, "output scale %d\n", c.demodStage.outputScale)

	if c.demodStage.outputScale < 1 {
		c.demodStage.outputScale = 1
	}

	if reflect.ValueOf(c.demodStage.modeDemod).Pointer() == reflect.ValueOf(fmDemod).Pointer() {
		c.demodStage.outputScale = 1
	}

	c.dongleStage.freq = uint32(captureFreq)
	c.dongleStage.rate = uint32(captureRate)
}
