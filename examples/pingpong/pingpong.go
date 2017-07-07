package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"regexp"

	"github.com/apache/beam/sdks/go/pkg/beam"
	"github.com/apache/beam/sdks/go/pkg/beam/io/textio"
	_ "github.com/apache/beam/sdks/go/pkg/beam/runners/dataflow"
	_ "github.com/apache/beam/sdks/go/pkg/beam/runners/dot"
	_ "github.com/apache/beam/sdks/go/pkg/beam/runners/local"
)

var (
	input  = flag.String("input", os.ExpandEnv("$GOPATH/src/github.com/apache/beam/sdks/go/data/haiku/old_pond.txt"), "Files to read.")
	output = flag.String("output", "/tmp/pingpong/out.", "Prefix of output.")

	runner = flag.String("runner", "local", "Pipeline runner.")
)

// stitch constructs two composite PTranformations that provide input to each other. It
// is a (deliberately) complex DAG to show what kind of structures are possible.
func stitch(p *beam.Pipeline, words beam.PCollection) (beam.PCollection, beam.PCollection) {
	ping := p.Composite("ping")
	pong := ping // p.Composite("pong")

	// NOTE(herohde) 2/23/2017: Dataflow does not allow cyclic composite structures.

	small1, big1 := beam.ParDo2(ping, multiFn, words, beam.SideInput{Input: words}) // self-sample (ping)
	small2, big2 := beam.ParDo2(pong, multiFn, words, beam.SideInput{Input: big1})  // big-sample  (pong). More words are small.
	_, big3 := beam.ParDo2(ping, multiFn, big2, beam.SideInput{Input: small1})      // small-sample big (ping). All words are big.
	small4, _ := beam.ParDo2(pong, multiFn, small2, beam.SideInput{Input: big3})    // big-sample small (pong). All words are small.

	return small4, big3
}

// Slice side input.

func multiFn(word string, sample []string, small, big func(string)) error {
	// TODO: side input processing into start bundle, once supported.

	count := 0
	size := 0
	for _, w := range sample {
		count++
		size += len(w)
	}
	if count == 0 {
		return errors.New("Empty sample")
	}
	avg := size / count

	if len(word) < avg {
		small(word)
	} else {
		big(word)
	}
	return nil
}

func subset(p *beam.Pipeline, a, b beam.PCollection) {
	beam.ParDo0(p, subsetFn, beam.Impulse(p), beam.SideInput{Input: a}, beam.SideInput{Input: b})
}

func subsetFn(_ []byte, a, b func(*string) bool) error {
	larger := make(map[string]bool)
	var elm string
	for b(&elm) {
		larger[elm] = true
	}
	for a(&elm) {
		if !larger[elm] {
			return fmt.Errorf("Extra element: %v", elm)
		}
	}
	return nil
}

var wordRE = regexp.MustCompile(`[a-zA-Z]+('[a-z])?`)

func extractFn(line string, emit func(string)) {
	for _, word := range wordRE.FindAllString(line, -1) {
		emit(word)
	}
}

func main() {
	flag.Parse()
	beam.Init()

	log.Print("Running pingpong")

	// PingPong constructs a convoluted pipeline with two "cyclic" composites.
	p := beam.NewPipeline()

	lines := textio.Read(p, *input)
	words := beam.ParDo(p, extractFn, lines)

	// Run baseline and stitch; then compare them.
	small, big := beam.ParDo2(p, multiFn, words, beam.SideInput{Input: words})
	small2, big2 := stitch(p, words)

	subset(p, small, small2)
	subset(p, big2, big)

	textio.Write(p, *output, small2)
	textio.Write(p, *output, big2)

	if err := beam.Run(context.Background(), *runner, p); err != nil {
		log.Fatalf("Failed to execute job: %v", err)
	}
}
