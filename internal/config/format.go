package config

import (
	"strconv"
)

func FormatKB(kb uint64) string {
	return strconv.FormatUint(kb, 10) + " kB"
}
