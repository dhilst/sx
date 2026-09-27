package main

import (
	"errors"
	"fmt"
)

func wrap(e error) error { return fmt.Errorf("%w", e) }

var _, _ = fmt.Errorf, errors.New
