.PHONY: all clean

BINDIR := bin

all: $(BINDIR)/consolidatefiles $(BINDIR)/deleteemptydirs $(BINDIR)/splitdir

$(BINDIR)/consolidatefiles: cmd/consolidatefiles/main.go
	go build -o $@ ./cmd/consolidatefiles

$(BINDIR)/deleteemptydirs: cmd/deleteemptydirs/main.go
	go build -o $@ ./cmd/deleteemptydirs

$(BINDIR)/splitdir: cmd/splitdir/main.go
	go build -o $@ ./cmd/splitdir

clean:
	rm -rf $(BINDIR)
