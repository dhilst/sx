package main

import (
	"errors"
	"fmt"
)

func wrap(e error) error { return errors.New(e.Error()) }

var _, _ = fmt.Errorf, errors.New
