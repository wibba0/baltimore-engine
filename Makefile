# Convenience wrapper around run.sh / go tooling.
.PHONY: build run html ui app test bench fetch clean

build: ; ./run.sh build
run:   ; ./run.sh run
html:  ; ./run.sh html
ui:    ; ./run.sh ui
app:   ; ./run.sh app
test:  ; ./run.sh test
bench: ; ./run.sh bench
fetch: ; ./run.sh fetch
clean: ; rm -rf bin dist report.html report.txt
