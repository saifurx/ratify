package search

import (
	"fmt"
	"strconv"
)

func itoa(n int) string                 { return strconv.Itoa(n) }
func sprintf(f string, a ...any) string { return fmt.Sprintf(f, a...) }
