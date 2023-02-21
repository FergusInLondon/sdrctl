package main

import (
	"context"
	"fmt"
	"os"
	"sync"
)

func newDemodStage(ctx context.Context, cfg *config) (*demodState, error) {
	c, can := context.WithCancel(context.Background())
	rate, err := cfg.sampleRate()

	return &demodState{
		ctx: c, cancel: can,
		rateIn: rate, rateOut: rate, agcEnable: cfg.Params.AGC,
		conseqSquelch: 10, squelchHits: 11, squelchLevel: cfg.Params.Squelch,
		postDownsample: 1,
		agc: struct {
			gainNum    int32
			gainDen    int32
			gainMax    int32
			peakTarget int
			attackStep int
			decayStep  int
			err        int
		}{
			gainDen:    1 << 15,
			gainNum:    1 << 15,
			peakTarget: 1 << 14,
			gainMax:    256 * (1 << 15),
			decayStep:  1,
			attackStep: -2,
		},
	}, err
}

func (d *demodState) stop() {
	d.cancel()
}

type demodState struct {
	ctx       context.Context
	cancel    context.CancelFunc
	lowpassed []int16
	rateIn    int
	rateOut   int
	rateOut2  int
	nowR      int16
	nowJ      int16
	preR      int16
	preJ      int16
	prevIndex int
	// min 1, max 256
	downsample     int
	postDownsample int
	outputScale    int
	squelchLevel   int
	conseqSquelch  int
	squelchHits    int
	customAtan     int
	deemph         bool
	deemphA        int
	nowLpr         int
	prevLprIndex   int
	modeDemod      func(fm *demodState)
	agcEnable      bool
	agc            struct {
		gainNum    int32
		gainDen    int32
		gainMax    int32
		peakTarget int
		attackStep int
		decayStep  int
		err        int
	}
}

func (d *demodState) routine(
	wg *sync.WaitGroup,
	fromDongle, toOutput chan []int16,
	hopChan chan struct{},
) (func(), error) {
	return func() {
		defer wg.Done()

		for {
			select {
			case <-d.ctx.Done():
				fmt.Fprintln(os.Stderr, "[demod stage] ctx cancelled. returning...")
				return
			case d.lowpassed = <-fromDongle:
				d.fullDemod()

				if d.squelchLevel > 0 && d.squelchHits > d.conseqSquelch {
					// hair trigger
					d.squelchHits = d.conseqSquelch + 1
					hopChan <- struct{}{}
					continue
				}
				result := make([]int16, len(d.lowpassed))
				copy(result, d.lowpassed)
				toOutput <- result
			}
		}
	}, nil
}

func (d *demodState) fullDemod() {
	var i int
	doSquelch := false

	lowPass(d)

	// power squelch
	if d.squelchLevel > 0 {
		sr := rms(d.lowpassed, 1)
		if sr < d.squelchLevel {
			doSquelch = true
		}
	}

	if doSquelch {
		d.squelchHits++
		for i = 0; i < len(d.lowpassed); i++ {
			d.lowpassed[i] = 0
		}
	} else {
		d.squelchHits = 0
	}

	if d.squelchLevel > 0 && d.squelchHits > d.conseqSquelch {
		d.agc.gainNum = d.agc.gainDen
	}

	d.modeDemod(d)
	if d.agcEnable {
		softwareAgc(d)
	}
	if d.deemph {
		deemphFilter(d)
	}
	if d.rateOut2 > 0 {
		lowPassReal(d)
	}
}
