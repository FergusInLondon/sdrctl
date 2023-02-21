package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/ini.v1"
)

const (
	CONFIG_FILE_ENV_VAR          = "SDRCTL_CONFIG_FILE"
	CONFIG_FILE_DEFAULT_LOCATION = "/etc/sdrctl/conf.ini"
	MAX_FREQS_LIMIT              = 1000
)

var (
	errNoConfigFound     = errors.New("unable to find valid configuration file")
	errInvalidConfigFreq = errors.New("invalid parameters for frequency")
)

type cfgNetIface struct {
	ListenHost string
	ListenPort int
	Network    string
	BasicAuth  struct {
		Username string
		Password string
	}
}

type config struct {
	Database       string
	CtrlInterface  cfgNetIface
	AudioInterface cfgNetIface
	AudioOutput    struct {
		SampleRate string
		PadGaps    bool
	}
	Scanner struct {
		DongleSerial string
	}
	Params struct {
		DemodMode    string
		Freq         string
		ScanBegin    string
		ScanEnd      string
		StepDuration string
		Step         string
		Squelch      int
		PPMError     int
		Gain         int
		AGC          bool
	}
}

func getConfigFileLocation(cliFlag string) string {
	if cliFlag != "" {
		return cliFlag
	}

	if envFile := os.Getenv(CONFIG_FILE_ENV_VAR); envFile != "" {
		return envFile
	}

	return CONFIG_FILE_DEFAULT_LOCATION
}

func getDefaults() config {
	return config{
		Database: "/etc/sdrctl/data.db",
		CtrlInterface: cfgNetIface{
			ListenHost: "localhost",
			ListenPort: 8081,
			BasicAuth: struct {
				Username string
				Password string
			}{
				Username: "admin", Password: "",
			},
		},
		AudioInterface: cfgNetIface{
			ListenHost: "localhost",
			ListenPort: 8080,
		},
		AudioOutput: struct {
			SampleRate string
			PadGaps    bool
		}{
			SampleRate: "24k",
			PadGaps:    false,
		},
		Params: struct {
			DemodMode    string
			Freq         string
			ScanBegin    string
			ScanEnd      string
			StepDuration string
			Step         string
			Squelch      int
			PPMError     int
			Gain         int
			AGC          bool
		}{
			DemodMode: "am",
			Squelch:   0,
			PPMError:  0,
			Gain:      -100,
			AGC:       false,
		},
	}
}

func getConfig(cliFlag *string) (*config, error) {
	var cfg = getDefaults()

	if err := ini.MapToWithMapper(&cfg, ini.TitleUnderscore, getConfigFileLocation(*cliFlag)); err != nil {
		if os.IsNotExist(err) {
			return nil, errNoConfigFound
		}

		return nil, err
	}

	return &cfg, nil
}

func (c *config) sampleRate() (int, error) {
	f, e := c.freqHz(c.AudioOutput.SampleRate)
	return int(f), e
}

func (c *config) listenFreqs() ([]uint32, error) {
	if c.Params.Freq != "" {
		f, err := c.freqHz(c.Params.Freq)
		return []uint32{f}, err
	}

	// @ugly @refactor
	start, err := c.freqHz(c.Params.ScanBegin)
	step, err1 := c.freqHz(c.Params.Step)
	end, err2 := c.freqHz(c.Params.ScanEnd)
	if err != nil || err1 != nil || err2 != nil {
		fmt.Fprintf(os.Stderr, "unable to parse frequencies from config: %s", err)
		return []uint32{}, errInvalidConfigFreq
	}

	freqs := make([]uint32, 1)
	for ; start < end && len(freqs) <= MAX_FREQS_LIMIT; start += step {
		freqs = append(freqs, start)
	}

	return freqs, nil
}

func (c *config) freqHz(freqStr string) (uint32, error) {
	val := strings.ToUpper(freqStr)

	if strings.HasSuffix(val, "K") {
		v := strings.TrimSuffix(val, "K")
		f64, err := strconv.ParseFloat(v, 64)
		return uint32(f64 * 1e3), err
	}

	if strings.HasSuffix(val, "M") {
		v := strings.TrimSuffix(val, "M")
		f64, err := strconv.ParseFloat(v, 64)
		return uint32(f64 * 1e6), err
	}

	if last := len(val) - 1; last >= 0 {
		val = val[:last]
	}
	f64, err := strconv.ParseFloat(val, 64)
	return uint32(f64), err
}
