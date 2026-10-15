# Convenience wrapper around run.sh / go tooling.
.PHONY: build run html test bench fetch clean

build: ; ./run.sh build
run:   ; ./run.sh run
html:  ; ./run.sh html
test:  ; ./run.sh test
bench: ; ./run.sh bench
fetch: ; ./run.sh fetch
clean: ; rm -rf bin report.html report.txt
