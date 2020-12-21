// +build go1.17

package litecmp

import (
	"os"
	"runtime"
	"strings"
)

func init() {
	dots := strings.SplitN(runtime.Version(), ".", 3)
	v := runtime.Version()
	if len(dots) >= 2 {
		v = dots[0] + "." + dots[1]
	}
	if os.Getenv("LITECMP_UNSAFE_RISK_IT_WITH") == v {
		return
	}
	panic("The litecmp package plays unsafe games and your version is untested with the " + v + " runtime. If you want to risk it, run with environment variable LITECMP_UNSAFE_RISK_IT_WITH=" + v + " set. Notably, if " + v + " adds a moving garbage collector, this package is unsafe.")
}
