package rpc

import (
	"bufio"
	"errors"
	"io"
)

var errMessageTooLarge = errors.New("message exceeds maximum size")

func readLine(reader *bufio.Reader, maxBytes int) ([]byte, error) {
	line := make([]byte, 0, min(maxBytes, 4096))
	overflow := false
	for {
		fragment, err := reader.ReadSlice('\n')
		if !overflow {
			remaining := maxBytes - len(line)
			if len(fragment) > remaining {
				overflow = true
			} else {
				line = append(line, fragment...)
			}
		}
		if err == nil {
			if overflow {
				return nil, errMessageTooLarge
			}
			return line, nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(err, io.EOF) {
			if overflow {
				return nil, errMessageTooLarge
			}
			if len(line) > 0 {
				return line, nil
			}
			return nil, io.EOF
		}
		return nil, err
	}
}
