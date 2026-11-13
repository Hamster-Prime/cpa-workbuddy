package workbuddy

import "bytes"

// eventStreamDecoder owns only its bounded in-flight event. Event slices borrow
// its storage and must be consumed before the callback returns.
type eventStreamDecoder struct {
	line, data, fields []byte
	name               string
	skipLF, hasData    bool
}

type streamEvent struct {
	data, fields []byte
	name         string
	hasData      bool
}

func (d *eventStreamDecoder) feed(payload []byte, process func(streamEvent) (bool, error), tooLarge func() error) (bool, error) {
	for _, b := range payload {
		if d.skipLF {
			d.skipLF = false
			if b == '\n' {
				continue
			}
		}
		if b != '\r' && b != '\n' {
			if len(d.line) == chatEventLimit {
				return false, tooLarge()
			}
			d.line = append(d.line, b)
			continue
		}
		d.skipLF = b == '\r'
		if len(d.line) == 0 {
			data := d.data
			if d.hasData {
				data = data[:len(data)-1]
			}
			done, err := process(streamEvent{data: data, fields: d.fields, name: d.name, hasData: d.hasData})
			d.data, d.fields = d.data[:0], d.fields[:0]
			d.name, d.hasData = "", false
			if err != nil || done {
				return done, err
			}
		} else if d.line[0] != ':' {
			key, value, _ := bytes.Cut(d.line, []byte(":"))
			if len(value) > 0 && value[0] == ' ' {
				value = value[1:]
			}
			switch string(key) {
			case "data":
				if len(d.data)+len(d.fields)+len(value)+1 > chatEventLimit {
					return false, tooLarge()
				}
				d.data = append(d.data, value...)
				d.data = append(d.data, '\n')
				d.hasData = true
			case "event", "id", "retry":
				if len(d.data)+len(d.fields)+len(d.line)+1 > chatEventLimit {
					return false, tooLarge()
				}
				d.fields = append(d.fields, d.line...)
				d.fields = append(d.fields, '\n')
				if string(key) == "event" {
					d.name = string(value)
				}
			}
		}
		d.line = d.line[:0]
	}
	return false, nil
}
