RTLSDR_PATH=/usr/local/rtl-sdr

.PHONY: run

.DEFAULT_GOAL: sdrctl

sdrctl: clean
	CGO_LDFLAGS="-lrtlsdr -L$(RTLSDR_PATH)/lib/" CGO_CPPFLAGS="-I$(RTLSDR_PATH)/include" go build

clean:
	rm -f ./sdrctl

run:
	LD_LIBRARY_PATH=$(RTLSDR_PATH)/lib/ ./sdrctl
