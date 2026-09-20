package control

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
)

const maxMessageSize = 1 << 20

type Request struct {
	Command string          `json:"command"`
	Payload json.RawMessage `json:"payload,omitempty"`
}
type Response struct {
	OK      bool            `json:"ok"`
	Error   string          `json:"error,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

func writeMessage(w io.Writer, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(b) > maxMessageSize {
		return fmt.Errorf("control message exceeds %d bytes", maxMessageSize)
	}
	b = append(b, '\n')
	_, err = w.Write(b)
	return err
}
func readMessage(r io.Reader, dst any) error {
	b, err := bufio.NewReader(io.LimitReader(r, maxMessageSize+1)).ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("read control message: %w", err)
	}
	if len(b) > maxMessageSize {
		return fmt.Errorf("control message exceeds %d bytes", maxMessageSize)
	}
	return json.Unmarshal(b, dst)
}
