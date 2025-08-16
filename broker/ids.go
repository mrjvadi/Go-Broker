package broker

import (
	"os"
	"strconv"
	"sync/atomic"
)

var (
	seq      atomic.Uint64
	idPrefix = func() string {
		h, _ := os.Hostname()
		if h == "" {
			h = "h"
		}
		return h + "-"
	}()
)

func nextID() string {
	n := seq.Add(1)
	return idPrefix + strconv.FormatUint(n, 36)
}
