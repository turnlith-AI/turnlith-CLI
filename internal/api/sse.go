package api

import (
	"bufio"
	"io"
	"strings"
)

// SSEReader reads Server-Sent Events from a stream. It yields the joined
// data payload of each event; comments (": ping"), event:, and id: fields
// are skipped.
type SSEReader struct {
	r *bufio.Reader
}

func NewSSEReader(r io.Reader) *SSEReader {
	return &SSEReader{r: bufio.NewReaderSize(r, 64*1024)}
}

// Next returns the next event's data, or io.EOF when the stream ends.
func (s *SSEReader) Next() (string, error) {
	var data []string
	for {
		line, err := s.r.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				if len(line) == 0 && len(data) == 0 {
					return "", io.EOF
				}
				if trimmed := trimLine(line); trimmed != "" {
					if payload, ok := dataPayload(trimmed); ok {
						data = append(data, payload)
					}
				}
				if len(data) > 0 {
					return strings.Join(data, "\n"), nil
				}
				return "", io.EOF
			}
			return "", err
		}
		trimmed := trimLine(line)
		if trimmed == "" {
			if len(data) > 0 {
				return strings.Join(data, "\n"), nil
			}
			continue
		}
		if strings.HasPrefix(trimmed, ":") {
			continue
		}
		if payload, ok := dataPayload(trimmed); ok {
			data = append(data, payload)
		}
	}
}

func trimLine(line string) string {
	return strings.TrimRight(line, "\r\n")
}

// dataPayload extracts the value of a "data:" field, reporting whether the
// line was one.
func dataPayload(line string) (string, bool) {
	if !strings.HasPrefix(line, "data:") {
		return "", false
	}
	return strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "), true
}
