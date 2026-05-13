package anthropicmessages

import (
	"bufio"
	"bytes"
	"io"
	"strings"
)

type sseFrame struct {
	Event string
	Data  []byte
}

type sseDecoder struct {
	scanner *bufio.Scanner
	event   string
	data    bytes.Buffer
}

func newSSEDecoder(r io.Reader) *sseDecoder {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	return &sseDecoder{scanner: scanner}
}

func (d *sseDecoder) Next() (sseFrame, error) {
	for d.scanner.Scan() {
		line := d.scanner.Text()
		if line == "" {
			if d.event != "" || d.data.Len() > 0 {
				return d.flush(), nil
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			d.event = value
		case "data":
			if d.data.Len() > 0 {
				d.data.WriteByte('\n')
			}
			d.data.WriteString(value)
		}
	}
	if err := d.scanner.Err(); err != nil {
		return sseFrame{}, err
	}
	if d.event != "" || d.data.Len() > 0 {
		return d.flush(), nil
	}
	return sseFrame{}, io.EOF
}

func (d *sseDecoder) flush() sseFrame {
	frame := sseFrame{Event: d.event, Data: append([]byte(nil), d.data.Bytes()...)}
	d.event = ""
	d.data.Reset()
	return frame
}

type eventEnvelope struct {
	Type string `json:"type"`
}
