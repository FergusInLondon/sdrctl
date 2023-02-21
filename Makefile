RTLSDR_PATH=/usr/local/rtl-sdr
INSTALL_PATH=/usr/local/bin

.PHONY: run install

.DEFAULT_GOAL: sdrctl

sdrctl: clean
	CGO_LDFLAGS="-lrtlsdr -L$(RTLSDR_PATH)/lib/" CGO_CPPFLAGS="-I$(RTLSDR_PATH)/include" go build

clean:
	rm -f ./sdrctl

run:
	LD_LIBRARY_PATH=$(RTLSDR_PATH)/lib/ ./sdrctl -c example.ini

install:
	cp sdrctl $(INSTALL_PATH)/sdrctl.bin
	echo "#!/bin/sh\nLD_LIBRARY_PATH=$(RTLSDR_PATH)/lib/ $(INSTALL_PATH)/sdrctl.bin" > $(INSTALL_PATH)/sdrctl
	chmod +x $(INSTALL_PATH)/sdrctl
