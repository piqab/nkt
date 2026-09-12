package portprobe

import (
	"context"
	"fmt"
	"github.com/piqab/nkt/internal/msgs"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/collect"
)

// Runner выполняет команду на хосте.
type Runner func(ctx context.Context, argv ...string) (collect.CommandResult, error)

// RunCurl запускает настоящий curl с аргументами оператора.
//
// Без оболочки: строка разбирается на аргументы здесь (кавычки и
// экранирование как в sh), и curl получает их напрямую — «;», «|» и
// подстановки остаются просто символами. Таймаут — свой «-m», если
// оператор его не задал, плюс контекст: curl, зависший на молчащем
// порту, не должен висеть вместе с запросом.
func RunCurl(ctx context.Context, run Runner, r Request) Result {
	res := Result{Command: Command(r)}
	if err := r.Validate(); err != nil {
		res.Error = err.Error()
		return res
	}
	if run == nil {
		res.Error = msgs.Tc(ctx, "portprobe.curlUnavailableMode")
		return res
	}
	args, err := SplitArgs(r.Args)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	if !hasTimeoutFlag(args) {
		args = append([]string{"-m", fmt.Sprintf("%d", r.TimeoutS)}, args...)
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(r.TimeoutS+2)*time.Second)
	defer cancel()

	started := time.Now()
	out, err := run(ctx, append([]string{"curl"}, args...)...)
	res.ElapsedMS = time.Since(started).Milliseconds()
	if err != nil {
		res.Error = "curl: " + err.Error()
		return res
	}
	res.OK = out.ExitCode == 0
	res.Body = []byte(out.Stdout)
	res.ContentType = "text/plain"
	if len(res.Body) > maxBody {
		res.Body = res.Body[:maxBody]
		res.Truncated = true
	}
	if out.Stderr != "" {
		res.Status = strings.TrimSpace(lastLine(out.Stderr))
	}
	if !res.OK && res.Error == "" {
		res.Error = msgs.Tc(ctx, "portprobe.curlExitedCode", out.ExitCode)
		if msg := strings.TrimSpace(out.Stderr); msg != "" {
			res.Error += ": " + lastLine(msg)
		}
	}
	return res
}

func hasTimeoutFlag(args []string) bool {
	for _, a := range args {
		if a == "-m" || a == "--max-time" || strings.HasPrefix(a, "--max-time=") {
			return true
		}
	}
	return false
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// SplitArgs разбирает строку на аргументы по правилам оболочки: пробелы
// разделяют, одинарные кавычки берут всё буквально, двойные — с
// экранированием обратной косой, обратная косая экранирует следующий
// символ. Подстановок и перенаправлений нет: это не оболочка.
func SplitArgs(s string) ([]string, error) {
	var (
		args    []string
		cur     strings.Builder
		inArg   bool
		quote   rune
		escaped bool
	)
	for _, ch := range s {
		switch {
		case escaped:
			cur.WriteRune(ch)
			escaped = false
			inArg = true
		case quote == '\'':
			if ch == '\'' {
				quote = 0
			} else {
				cur.WriteRune(ch)
			}
		case quote == '"':
			switch ch {
			case '"':
				quote = 0
			case '\\':
				escaped = true
			default:
				cur.WriteRune(ch)
			}
		case ch == '\\':
			escaped = true
		case ch == '\'' || ch == '"':
			quote = ch
			inArg = true
		case ch == ' ' || ch == '\t' || ch == '\n':
			if inArg {
				args = append(args, cur.String())
				cur.Reset()
				inArg = false
			}
		default:
			cur.WriteRune(ch)
			inArg = true
		}
	}
	if quote != 0 {
		return nil, msgs.Errorf("portprobe.unclosedQuoteArguments")
	}
	if escaped {
		return nil, msgs.Errorf("portprobe.trailingBackslashEndLine")
	}
	if inArg {
		args = append(args, cur.String())
	}
	if len(args) == 0 {
		return nil, msgs.Errorf("portprobe.specifyCurlArguments")
	}
	return args, nil
}
