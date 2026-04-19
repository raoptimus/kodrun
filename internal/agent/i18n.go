/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package agent

// planLabel returns the localized string for a plan-related key.
// Falls back to English if the language or key is not found.
func planLabel(lang, key string) string {
	if m, ok := planLabels[lang]; ok {
		if s, ok := m[key]; ok {
			return s
		}
	}
	return planLabels["en"][key]
}

var planLabels = map[string]map[string]string{
	"en": {
		"tasks":         "## Tasks\n",
		"tasks2":        "## Tasks\n\n",
		"affected":      "\n## Affected files\n",
		"affected2":     "## Affected files\n\n",
		"verification":  "\n## Post-execution verification\n",
		"verification2": "## Post-execution verification\n\n",
		"lines":         " (lines: ",
		"build":         "Build project",
		"lint":          "Run linter",
		"test":          "Run tests",
	},
	"ru": {
		"tasks":         "## Задачи\n",
		"tasks2":        "## Задачи\n\n",
		"affected":      "\n## Затронутые файлы\n",
		"affected2":     "## Затронутые файлы\n\n",
		"verification":  "\n## Проверка после выполнения\n",
		"verification2": "## Проверка после выполнения\n\n",
		"lines":         " (строки: ",
		"build":         "Собрать проект",
		"lint":          "Запустить линтер",
		"test":          "Запустить тесты",
	},
}
