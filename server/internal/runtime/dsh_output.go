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
	failure  string
}

func (o *DSHFinalOutput) Add(chunk string) {
	// Execd can send each stdout line as its own event without the newline.
	if len(chunk) <= maxDSHRecord && !strings.Contains(chunk, "\n") && o.consumeRecord([]byte(chunk)) {
		o.resetLine()
		return
	}
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
			if len(o.line) != 0 && o.line[len(o.line)-1] == '}' && o.consumeRecord(o.line) {
				o.resetLine()
			}
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

func (o *DSHFinalOutput) Failure() string {
	o.finishLine()
	return o.failure
}

func (o *DSHFinalOutput) finishLine() {
	if len(o.line) != 0 {
		o.consumeRecord(o.line)
	}
	o.resetLine()
}

func (o *DSHFinalOutput) resetLine() {
	o.line = o.line[:0]
	o.oversize = false
}

func (o *DSHFinalOutput) consumeRecord(record []byte) bool {
	var event struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Message string `json:"message"`
	}
	if json.Unmarshal(record, &event) != nil || event.Type == "" {
		return false
	}
	switch event.Type {
	case "final":
		o.final = event.Text
	case "error":
		o.failure = event.Message
	}
	return true
}
