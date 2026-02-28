PKG_CONFIG_PATH := ./pkgconfig:$(PKG_CONFIG_PATH)
export PKG_CONFIG_PATH

build:
	go build -o sn-monitor .

run: build
	./sn-monitor

clean:
	rm -f sn-monitor
