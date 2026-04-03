package tui

// Locale provides localized UI strings.
type Locale struct {
	lang    string
	strings map[string]string
}

// NewLocale creates a locale for the given language code.
// Falls back to English for unknown languages.
func NewLocale(lang string) *Locale {
	strings, ok := translations[lang]
	if !ok {
		strings = translations["en"]
	}
	return &Locale{lang: lang, strings: strings}
}

// Get returns the localized string for the given key.
// Returns the key itself if not found.
func (l *Locale) Get(key string) string {
	if s, ok := l.strings[key]; ok {
		return s
	}
	// Fallback to English.
	if s, ok := translations["en"][key]; ok {
		return s
	}
	return key
}

var translations = map[string]map[string]string{
	"en": {
		// Placeholders
		"placeholder.plan":       "Describe task for planning...",
		"placeholder.edit":       "Enter task or /command...",
		"placeholder.constraint": "Describe constraint...",

		// Confirm
		"confirm.opt.allow_once":    "Yes, proceed",
		"confirm.opt.allow_session": "Yes, don't ask again for this command",
		"confirm.opt.edit":          "Edit / add constraint",
		"confirm.opt.deny":          "No, deny",
		"confirm.card.tool":         "Tool:",
		"confirm.card.preview":      "preview:",
		"confirm.card.danger":       "WARNING: dangerous command",
		"confirm.hint":              "↑↓ navigate · enter confirm · esc cancel",
		"confirm.enter_constraint":  "  ↩ enter constraint (Esc to cancel):",
		"confirm.augment":           "  ↩ augment: ",

		// Status
		"status.switched_edit": "Switched to edit mode (send message to start)",
		"status.switched_plan": "Switched to plan mode (think enabled)",
		"status.cancelled":     "cancelled",
		"status.task_running":  "task already running, press Esc to cancel",
		"status.mouse_on":      "mouse capture enabled",
		"status.mouse_off":     "mouse capture disabled",
		"status.context_dump":  "── context dump ──",
		"status.context_end":   "── end context ──",
		"status.init":          "Initializing...",
		"status.working":       "Working",

		// Labels
		"label.plan_mode":    "plan mode on",
		"label.edit_mode":    "accept edits on",
		"label.shift_tab":    "(shift+tab)",
		"label.context_left": "%d%% left",

		// Events
		"event.fix":     "[fix]   ",
		"event.error":   "[error] ",
		"event.compact": "[compact] ",
		"event.done":    "[done]  ",

		// Summary
		"summary.files":      "\n  Files: ",
		"summary.lines":      "\n  Lines: +%d -%d",
		"summary.added":      "%d added",
		"summary.modified":   "%d modified",
		"summary.deleted":    "%d deleted",
		"summary.renamed":    "%d renamed",
		"summary.tool_calls": "%d tool calls",
		"summary.avg_tks":    "%.1f avg tk/s",
		"summary.peak_ctx":   "peak %d%% ctx",

		// Avatars
		"avatar.user":  "You",
		"avatar.agent": "Agent",

		// Diff
		"diff.more_lines": "  ... (%d more lines)",

		// Plan confirm
		"plan_confirm.header":         "Agent has written up a plan and is ready to execute. Would you like to proceed?",
		"plan_confirm.auto_accept":    "Yes, auto-accept edits",
		"plan_confirm.manual_approve": "Yes, manually approve edits",
		"plan_confirm.augment":        "Type here to tell Agent what to change",
		"plan_confirm.hint":           "↑↓ navigate · enter confirm · esc cancel",
		"plan_confirm.enter_feedback": "  ↩ enter feedback (Esc to cancel):",
		"plan_confirm.feedback":       "  ↩ feedback: ",

		// Group
		"group.more_tools": "  +%d more tool uses (ctrl+o to expand)",
	},
	"ru": {
		// Placeholders
		"placeholder.plan":       "Опишите задачу для планирования...",
		"placeholder.edit":       "Введите задачу или /команду...",
		"placeholder.constraint": "Опишите ограничение...",

		// Confirm
		"confirm.opt.allow_once":    "Да, выполнить",
		"confirm.opt.allow_session": "Да, больше не спрашивать для этой команды",
		"confirm.opt.edit":          "Изменить / добавить ограничение",
		"confirm.opt.deny":          "Нет, отказать",
		"confirm.card.tool":         "Инструмент:",
		"confirm.card.preview":      "предпросмотр:",
		"confirm.card.danger":       "ВНИМАНИЕ: опасная команда",
		"confirm.hint":              "↑↓ навигация · enter подтвердить · esc отмена",
		"confirm.enter_constraint":  "  ↩ введите ограничение (Esc для отмены):",
		"confirm.augment":           "  ↩ дополнение: ",

		// Status
		"status.switched_edit": "Режим правок (отправьте сообщение для начала)",
		"status.switched_plan": "Режим плана (think включён)",
		"status.cancelled":     "отменено",
		"status.task_running":  "задача выполняется, нажмите Esc для отмены",
		"status.mouse_on":      "захват мыши включён",
		"status.mouse_off":     "захват мыши выключен",
		"status.context_dump":  "── дамп контекста ──",
		"status.context_end":   "── конец контекста ──",
		"status.init":          "Инициализация...",
		"status.working":       "Работаю",

		// Labels
		"label.plan_mode":    "режим плана",
		"label.edit_mode":    "режим правок",
		"label.shift_tab":    "(shift+tab)",
		"label.context_left": "%d%% свободно",

		// Events
		"event.fix":     "[исправление] ",
		"event.error":   "[ошибка] ",
		"event.compact": "[компакт] ",
		"event.done":    "[готово] ",

		// Summary
		"summary.files":      "\n  Файлы: ",
		"summary.lines":      "\n  Строки: +%d -%d",
		"summary.added":      "%d добавлено",
		"summary.modified":   "%d изменено",
		"summary.deleted":    "%d удалено",
		"summary.renamed":    "%d переименовано",
		"summary.tool_calls": "%d вызовов",
		"summary.avg_tks":    "%.1f сред. tk/с",
		"summary.peak_ctx":   "пик %d%% ctx",

		// Avatars
		"avatar.user":  "Вы",
		"avatar.agent": "Агент",

		// Diff
		"diff.more_lines": "  ... (ещё %d строк)",

		// Plan confirm
		"plan_confirm.header":         "Агент подготовил план и готов к выполнению. Продолжить?",
		"plan_confirm.auto_accept":    "Да, авто-принять правки",
		"plan_confirm.manual_approve": "Да, подтверждать правки вручную",
		"plan_confirm.augment":        "Написать что изменить",
		"plan_confirm.hint":           "↑↓ навигация · enter подтвердить · esc отмена",
		"plan_confirm.enter_feedback": "  ↩ введите замечание (Esc для отмены):",
		"plan_confirm.feedback":       "  ↩ замечание: ",

		// Group
		"group.more_tools": "  +%d вызовов (ctrl+o развернуть)",
	},
}
