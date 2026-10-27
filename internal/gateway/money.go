package gateway

import (
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
)

const USDScale int64 = 1_000_000_000

var decimalUSD = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?(?:[eE][+-]?[0-9]{1,2})?$`)

// ParseUSD rounds nonnegative decimal USD to the nearest nano-dollar, ties up.
func ParseUSD(s string) (int64, error) {
	if len(s) == 0 || len(s) > 100 || !decimalUSD.MatchString(s) {
		return 0, errors.New("invalid decimal USD amount")
	}
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		n, err := strconv.Atoi(s[i+1:])
		if err != nil || n < -30 || n > 30 {
			return 0, errors.New("USD exponent out of range")
		}
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok || r.Sign() < 0 {
		return 0, errors.New("invalid nonnegative USD amount")
	}
	r.Mul(r, big.NewRat(USDScale, 1))
	q, rem := new(big.Int), new(big.Int)
	q.QuoRem(r.Num(), r.Denom(), rem)
	if rem.Mul(rem, big.NewInt(2)).Cmp(r.Denom()) >= 0 {
		q.Add(q, big.NewInt(1))
	}
	if !q.IsInt64() {
		return 0, errors.New("USD amount exceeds supported range")
	}
	return q.Int64(), nil
}

func FormatUSD(n int64) string {
	if n < 0 {
		return fmt.Sprintf("-%d.%09d", -(n / USDScale), -(n % USDScale))
	}
	return fmt.Sprintf("%d.%09d", n/USDScale, n%USDScale)
}

func tokenCost(input, output int64, r Route) (int64, error) {
	a, ok := new(big.Rat).SetString(r.InputUSDPerMillion)
	if !ok {
		return 0, errors.New("invalid input rate")
	}
	b, ok := new(big.Rat).SetString(r.OutputUSDPerMillion)
	if !ok {
		return 0, errors.New("invalid output rate")
	}
	a.Mul(a, big.NewRat(input, 1))
	b.Mul(b, big.NewRat(output, 1))
	a.Add(a, b)
	a.Quo(a, big.NewRat(1_000_000, 1))
	return ParseUSD(a.FloatString(18))
}
