package sx

import (
	"encoding/json"
	"io"
)

func Decode(r io.Reader) (*Graph, error) {
	var g Graph
	if err := json.NewDecoder(r).Decode(&g); err != nil {
		return nil, err
	}
	return &g, nil
}
