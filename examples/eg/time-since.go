//go:build ignore

package template

import "time"

func before(t time.Time) time.Duration { return time.Now().Sub(t) }
func after(t time.Time) time.Duration  { return time.Since(t) }
