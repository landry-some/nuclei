package progress

import (
	"fmt"
	"github.com/logrusorgru/aurora"
	"github.com/vbauerster/mpb/v5"
	"github.com/vbauerster/mpb/v5/cwriter"
	"github.com/vbauerster/mpb/v5/decor"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

type Progress struct {
	progress *mpb.Progress
	bar *mpb.Bar
	total int64
	initialTotal int64
	captureData *captureData
	termWidth int
}

func NewProgress(group *sync.WaitGroup) *Progress {
	w := cwriter.New(os.Stdout)
	tw, err := w.GetWidth()
	if err != nil {
		panic("Couldn't determine available terminal width.")
	}

	p := &Progress{
		progress: mpb.New(
			mpb.WithWaitGroup(group),
			mpb.WithOutput(os.Stderr),
			mpb.PopCompletedMode(),
		),
		termWidth: tw,
	}
	return p
}

func (p *Progress) SetupProgressBar(name string, total int64) *mpb.Bar {
	barname := "[" + aurora.Green(name).String() + "]"
	bar := p.progress.AddBar(
		total,
		mpb.BarNoPop(),
		mpb.BarRemoveOnComplete(),
		mpb.PrependDecorators(
			decor.Name(barname),
			decor.CountersNoUnit(aurora.Blue(" %d/%d").String()),
			decor.NewPercentage(aurora.Bold("%d").String(), decor.WCSyncSpace),
		),
		mpb.AppendDecorators(
			decor.AverageSpeed(0, aurora.Yellow("%.2f req/s ").String()),
			decor.OnComplete(
				decor.AverageETA(decor.ET_STYLE_GO), aurora.Bold("done!").String(),
			),
		),
	)

	p.bar = bar
	p.total = total
	p.initialTotal = total
	return bar
}

func (p *Progress) Update() {
	p.bar.Increment()
}

func (p *Progress) Abort(remaining int64) {
	p.total -= remaining
	p.bar.SetTotal(p.total, false)
}

func (p *Progress) Wait() {
	if p.initialTotal != p.total {
		p.bar.SetTotal(p.total, true)
	}

	p.progress.Wait()
}

//

func (p *Progress) StartStdCapture() {
	p.captureData = startStdCapture()
}

func (p *Progress) StopStdCaptureAndShow() {
	stopStdCapture(p.captureData)
	for _, captured := range p.captureData.Data {
		var r = regexp.MustCompile("(.{" + strconv.Itoa(p.termWidth) + "})")
		multiline := r.ReplaceAllString(captured, "$1\n")
		arr := strings.Split(multiline, "\n")

		for _, msg := range arr {
			p.progress.Add(0, makeLogBar(msg)).SetTotal(0, true)
		}
	}
}

func makeLogBar(msg string) mpb.BarFiller {
	return mpb.BarFillerFunc(func(w io.Writer, _ int, st decor.Statistics) {
		fmt.Fprintf(w, msg)
	})
}
