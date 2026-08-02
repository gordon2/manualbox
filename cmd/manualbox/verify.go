package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/gordon2/manualbox/internal/doc"
	"github.com/gordon2/manualbox/internal/verify"
)

// cmdVerify converts a PDF and reports what is wrong with the conversion.
//
// It exists to be run on a manual the fixtures do not contain, which is where
// this check earns its keep: the two measured documents are the two whose defects
// are already written down. Nothing is stored and nothing is uploaded — the same
// stance doctor takes — so it is safe to point at a file and read the answer.
//
// The document is converted for EVERY language it holds rather than for the
// household's, because coverage is measured against all the text on a page and a
// page of a parallel-columns manual holds five languages. See [verify.Check].
func cmdVerify(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	limit := fs.Int("limit", 20, "how many findings of each kind to print")
	all := fs.Bool("all", false, "print every finding rather than the first few of each kind")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: manualbox verify [-limit n] [-all] <file.pdf>")
	}
	path := fs.Arg(0)

	start := time.Now()
	res, err := doc.Analyze(ctx, path)
	if err != nil {
		return err
	}
	langs := res.Languages()
	names := make([]string, 0, len(langs))
	for i := range langs {
		names = append(names, langs[i].Lang)
	}
	fmt.Fprintf(stdout, "%d pages, %d language(s): %s\nprobed in %v\n",
		res.Info.Pages, len(langs), join(names), time.Since(start).Round(time.Millisecond))

	start = time.Now()
	conv, err := verify.ConvertAll(ctx, path, res)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%s\nconverted in %v\n", conv.Summary(),
		time.Since(start).Round(time.Millisecond))
	for _, n := range conv.Notes {
		fmt.Fprintf(stdout, "  note: %s\n", n)
	}

	start = time.Now()
	rep, err := verify.Check(ctx, path, conv)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "\n%s\nchecked in %v\n\n", rep.Summary(),
		time.Since(start).Round(time.Millisecond))
	for _, n := range rep.Notes {
		fmt.Fprintf(stdout, "  note: %s\n", n)
	}

	tw := tabwriter.NewWriter(stdout, 0, 8, 2, ' ', 0)
	fmt.Fprintf(tw, "  kind\tfindings\tpages\n")
	kinds := rep.Kinds()
	for _, k := range verify.AllKinds {
		if kinds[k] == 0 {
			continue
		}
		fmt.Fprintf(tw, "  %s\t%d\t%d\n", k, kinds[k], rep.PagesFlagged(k))
	}
	tw.Flush()

	// The worst pages by coverage, whether or not they were reported: the
	// measurement is what a person reads this for, and a document whose worst page
	// scores 0.99 has been told something by that number.
	cov := make([]verify.PageCoverage, len(rep.Coverage))
	copy(cov, rep.Coverage)
	sort.Slice(cov, func(a, b int) bool { return cov[a].Ratio < cov[b].Ratio })
	fmt.Fprintf(stdout, "\nmedian coverage %.3f; least covered pages:\n", rep.MedianCoverage())
	for i := 0; i < len(cov) && i < 5; i++ {
		fmt.Fprintf(stdout, "  page %d: %.3f (%d block characters against %d from pdftotext)\n",
			cov[i].Page, cov[i].Ratio, cov[i].Blocks, cov[i].Text)
	}

	shown := make(map[verify.Kind]int, len(kinds))
	fmt.Fprintln(stdout)
	for i := range rep.Findings {
		f := &rep.Findings[i]
		if !*all {
			if shown[f.Kind] >= *limit {
				continue
			}
			shown[f.Kind]++
		}
		fmt.Fprintf(stdout, "%s: %s\n", f.Kind, f.Detail)
		if f.Sample != "" {
			fmt.Fprintf(stdout, "    %s\n", f.Sample)
		}
	}
	if !*all {
		for k, n := range kinds {
			if n > shown[k] {
				fmt.Fprintf(stdout, "… %d more %s finding(s); -all prints them\n", n-shown[k], k)
			}
		}
	}
	return nil
}
