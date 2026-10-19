package money

import (
	"fmt"
	"strconv"
)

func FormatCents(cents int64) string {
	return strconv.FormatInt(cents/100, 10) + "." + fmt.Sprintf("%02d", cents%100)
}

func FormatSignedCents(cents int64) string {
	if cents < 0 {
		return "-" + FormatCents(-cents)
	}
	return FormatCents(cents)
}
