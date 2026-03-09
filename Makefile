export PKG_CONFIG_PATH := $(CURDIR)/pkgconfig:$(PKG_CONFIG_PATH)

build:
	go build -o sn-monitor .

run: build
	@./setup-mpx.sh || true
	@./sn-monitor; ./teardown-mpx.sh || true

clean:
	rm -f sn-monitor
