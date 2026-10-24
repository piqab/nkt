package script

import (
	"sync"

	"github.com/piqab/nkt/internal/msgs"
)

// Команды плагинов: «plugin ИМЯ СЛОВО аргументы…» — всегда, и слово
// команды подключённого плагина само по себе («dns set …»). Какие слова
// чьи — хаб задаёт при подключении и отключении плагинов (SetPluginWords).
// Шаг — KindPlugin: Name — плагин, List — слово и аргументы.

var (
	pluginMu    sync.RWMutex
	pluginWords = map[string]string{}
)

// SetPluginWords — слова команд подключённых плагинов: слово → плагин.
func SetPluginWords(words map[string]string) {
	pluginMu.Lock()
	defer pluginMu.Unlock()
	pluginWords = map[string]string{}
	for w, p := range words {
		if !BuiltinWord(w) {
			pluginWords[w] = p
		}
	}
}

func pluginWord(w string) (string, bool) {
	pluginMu.RLock()
	defer pluginMu.RUnlock()
	p, ok := pluginWords[w]
	return p, ok
}

// builtinWords — первые слова команд самого языка: плагину их не занять.
var builtinWords = map[string]bool{
	"set": true, "param": true, "group": true, "host": true, "install": true, "wait": true, "pipeline": true,
	"deploy": true, "migrate": true, "k8s": true, "fly": true, "on": true, "plugin": true, "end": true,
}

// BuiltinWord — слово занято языком сценариев.
func BuiltinWord(w string) bool { return builtinWords[w] || onCommands[w] }

// parsePlugin — «plugin ИМЯ СЛОВО …» или «СЛОВО …» плагина.
func parsePlugin(toks []string) (Step, bool, error) {
	if toks[0] == "plugin" {
		if len(toks) < 3 || !nameRe.MatchString(toks[1]) {
			return Step{}, true, msgs.Errorf("script.pluginUsage")
		}
		return Step{Kind: KindPlugin, Name: toks[1], List: toks[2:]}, true, nil
	}
	if p, ok := pluginWord(toks[0]); ok {
		return Step{Kind: KindPlugin, Name: p, List: toks}, true, nil
	}
	return Step{}, false, nil
}
