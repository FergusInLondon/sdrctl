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
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
)

const (
	defaultSampleRate = 24000
	defaultBufLen     = 16384
	maximumOversample = 16
	maximumBufLen     = (maximumOversample * defaultBufLen)
	autoGain          = -100
	bufferDump        = 4096
	minimumRate       = 1000000
)

func main() {
	var (
		cliCfgFile  = flag.String("c", "", "configuration file to load parameters from")
		pipelineCtx = context.Background()
		cfg         *config
		err         error
	)

	flag.Parse()
	if cfg, err = getConfig(cliCfgFile); err != nil {
		fmt.Fprintf(os.Stderr, "unable to read configuration: %s\n", err)
		return
	}

	dongleStage, _ := newDongleStage(pipelineCtx, cfg)

	demodStage, err := newDemodStage(pipelineCtx, cfg)
	handleErr("Unable to initialise demod stage %s\n", err)

	outputStage, err := newOutputStage(pipelineCtx, cfg)
	handleErr("Unable to initialise output stage %s\n", err)

	controller, err := newSDRController(
		pipelineCtx, cfg, dongleStage, demodStage, outputStage,
	)
	handleErr("Unable to initialise SDR controller %s\n", err)

	handleSignal(os.Interrupt, controller.stop)
	fmt.Fprintf(os.Stderr, "handing control over to sdr controller until SIGINT...\n")
	handleErr("SDR controller finished with error: %s\n", controller.run())
}

func handleErr(msg string, err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, msg, err)
		os.Exit(-1)
	}
}

func handleSignal(sig os.Signal, handleFn func()) {
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt)

	go func() {
		<-signalChan
		fmt.Fprintln(os.Stderr, "\nRecieved an interrupt, calling handler...")
		handleFn()
	}()
}
