package runtime

import (
	"encoding/json"
	"strings"
)

const maxDSHRecord = 1 << 20

// DSHFinalOutput extracts the latest final reply from headless DSH's JSON lines.
type DSHFinalOutput struct {
	line     []byte
	oversize bool
	final    string
}

func (o *DSHFinalOutput) Add(chunk string) {
	for chunk != "" {
		part, rest, complete := strings.Cut(chunk, "\n")
		if !o.oversize {
			if len(part) > maxDSHRecord-len(o.line) {
				o.oversize = true
				o.line = o.line[:0]
			} else {
				o.line = append(o.line, part...)
			}
		}
		if !complete {
			return
		}
		o.finishLine()
		chunk = rest
	}
}

func (o *DSHFinalOutput) Reply() string {
	o.finishLine()
	return o.final
}

func (o *DSHFinalOutput) finishLine() {
	if len(o.line) != 0 {
		var event struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(o.line, &event) == nil && event.Type == "final" {
			o.final = event.Text
		}
	}
	o.line = o.line[:0]
	o.oversize = false
}
