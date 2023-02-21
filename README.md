# `sdrctl` - network connected radio scanner (WIP)

A lightweight network daemon for using a RTL SDR a remote radio scanner; built upon the work contained in [porjo/hamsdr](https://github.com/porjo/hamsdr) - the best (and simplest) resource I've seen documenting working with RTL SDR devices in Go.

The goal is to provide a simple to configure daemon that can be used to control a RTL SDR device and stream received data; one such use case being installation on small systems such as Raspberry Pi Zero's that can be located in inaccessible places. *This isn't an alternative to (the closed-source) WebSDR, but I hope it could form part of a more scalable alternative.*

## Progress

At the time of amending this README this repository contains the original codebase forked from the above repository, with the addition of some build related niceties. For current progress see the [Github Project](https://github.com/users/FergusInLondon/projects/1/views/1).

## Running `sdrctl`

### Building and Installing Pre-Requisites (`librtlsdr`)

The `rtl-sdr` C library is required (and, as a result, `libusb` too); the latest-and-greatest can be downloaded from [librtlsdr/librtlsdr](https://github.com/librtlsdr/librtlsdr). The upstream codebase recommended that repository as a source as most distributions contained outdated versions at the time (~2017), nearly 5 years later I'm not sure if that remains the case though.

Assuming you wish to install the library to `/usr/local/rtl-sdr`:

```
$ git clone https://github.com/librtlsdr/librtlsdr
$ mkdir librtlsdr/build && cd librtlsdr/build
$ cmake -DCMAKE_INSTALL_PREFIX:PATH=/usr/local/rtl-sdr ../
$ make
$ sudo make install
```

### Building and Running `sdrctl`

Build and execution is all handled via the `Makefile`; note that by default the Makefile expects `librtlsdr` to be found in `/usr/local/rtl-sdr` - *if this is not the case then override it by providing RTLSDR_PATH as a flag to make*.

```
$ git clone git@github.com:FergusInLondon/sdrctl.git
$ cd sdrctl
$ make [RTLSDR_PATH=/path/to/rtlsdr/installation]
$ make [RTLSDR_PATH=/path/to/rtlsdr/installation] run
# if you're happy and want to install `sdrctl` to /usr/local/bin/:
$ sudo make install
```

#### Installing `sdrctl`

If the build appears functional and works as expected, then install it via `sudo make install`. This will install the binary in conjunction with a small shell script shim that sets the correct library path for `librtlsdr` prior to execution.

```
# optionally setting either the target dir and/or the librtlsdr dir
$ sudo make [RTLSDR_PATH=/path/to/rtlsdr/installation][INSTALL_PATH=/path/to/target/dir] install
```

## Credits

- The project is built upon [porjo/hamsdr](https://github.com/porjo/hamsdr), which provides solid foundations for RTL SDR interactions.
- In turn, the original works were inspired by `rtl_fm` from [rtl-sdr](https://github.com/keenerd/rtl-sdr) by Kyle Keen (@keenerd).

## License

Licensed under the terms of the **GNU General Public License (v2)** as per the original repository; please see [LICENSE.md](LICENSE.md) for full terms.
