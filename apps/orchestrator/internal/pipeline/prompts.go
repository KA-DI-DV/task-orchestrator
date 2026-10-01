package pipeline

import (
	"fmt"
	"strings"

	"orchestrator/internal/storage"
)

// Тут зібрані тексти завдань (prompts) для кожного агента.
//
// Роль і "характер" агента задаються в config.yaml (system_instruction),
// а тут — конкретне завдання: що за задача, що вже було, і в якому
// форматі відповісти, щоб оркестратор зміг автоматично зрозуміти результат.

// Маркери, які агенти мають написати останнім рядком відповіді.
const (
	verdictPrefix  = "VERDICT:"
	verdictApprove = "APPROVE"
	verdictChanges = "CHANGES_REQUESTED"
	testPrefix     = "TEST_RESULT:"
	testPassed     = "PASSED"
	testFailed     = "FAILED"
)

// Вердикти в історії задачі (task_logs.verdict), крім наведених вище.
const (
	verdictPlanReady   = "PLAN_READY"    // Архітектор підготував план
	verdictPlanOK      = "PLAN_APPROVED" // людина схвалила план
	verdictPlanChanges = "PLAN_CHANGES"  // людина написала правки до плану
	verdictDone        = "DONE"          // Developer завершив ітерацію
	verdictError       = "ERROR"         // агент або оркестратор завершився помилкою
	verdictInterrupted = "INTERRUPTED"   // задачу зупинили (Ctrl+C / зупинка контейнера)
)

// Заголовок розділу, у якому агент пояснює свій крок простою мовою.
// Оркестратор вирізає цей розділ з відповіді (extractBrief) і показує
// його в хронології адмін-панелі.
const briefHeading = "Підсумок простою мовою"

// briefSection — вимога до підсумку. points — що саме має бути в підсумку
// для цієї ролі.
func briefSection(points ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n## Підсумок (обов'язково)\nНаприкінці відповіді додай розділ `## %s` — для людини, яка не бачила коду.\n", briefHeading)
	b.WriteString("3–6 коротких пунктів звичайною мовою, без коду і шляхів до файлів (назви репозиторіїв — можна). У пунктах:\n")
	for _, p := range points {
		fmt.Fprintf(&b, "- %s\n", p)
	}
	return b.String()
}

func taskHeader(t *storage.Task) string {
	return fmt.Sprintf("Задача Jira %s: %s\n\nОпис задачі:\n%s\n", t.ID, t.Title, t.Description)
}

// workspaceSection описує агенту, які репозиторії є і де лежать їхні worktree.
func workspaceSection(t *storage.Task) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n## Репозиторії\nКожен репозиторій нижче — окремий git worktree на гілці %s:\n", t.BranchName)
	for _, r := range t.Repos {
		if r.IsMain {
			fmt.Fprintf(&b, "- %s — %s (основний, поточна папка)\n", r.RepoName, r.WorktreePath)
		} else {
			fmt.Fprintf(&b, "- %s — %s\n", r.RepoName, r.WorktreePath)
		}
	}
	return b.String()
}

// diffSection — як подивитися зміни в кожному зміненому репозиторії.
func diffSection(changed []storage.TaskRepo) string {
	if len(changed) == 0 {
		return "Змін немає в жодному репозиторії.\n"
	}
	var b strings.Builder
	b.WriteString("Зміни є в таких репозиторіях:\n")
	for _, r := range changed {
		fmt.Fprintf(&b, "- %s: git -C %s diff %s...HEAD\n", r.RepoName, r.WorktreePath, r.BaseCommit)
	}
	return b.String()
}

// planSection — затверджений план для Developer'а, Reviewer'а і QA.
// У старих задачах (до появи Архітектора) плану немає — розділ порожній.
func planSection(t *storage.Task) string {
	if strings.TrimSpace(t.Plan) == "" {
		return ""
	}
	return "\n## Затверджений план реалізації\nЦей план склав Архітектор, а людина його схвалила:\n\n" +
		strings.TrimSpace(t.Plan) + "\n"
}

func architectPrompt(t *storage.Task) string {
	var b strings.Builder
	b.WriteString("Ти — Architect. Проаналізуй задачу і код та склади план її реалізації.\n\n")
	b.WriteString(taskHeader(t))
	b.WriteString(workspaceSection(t))

	if t.PlanFeedback != "" && t.Plan != "" {
		fmt.Fprintf(&b, "\n## Попередня версія плану (версія %d)\n%s\n", t.PlanRevision, strings.TrimSpace(t.Plan))
		fmt.Fprintf(&b, "\n## Правки людини до цієї версії — обов'язково врахуй кожну\n%s\n", strings.TrimSpace(t.PlanFeedback))
		b.WriteString("\nПерероби план з урахуванням правок і поверни ПОВНИЙ новий план (а не лише змінені частини).\n" +
			"Одразу після заголовка додай розділ `## Що змінено у цій версії` — по пункту на кожну правку.\n")
	}

	b.WriteString(`
## Для кого план
План читає людина, яка поставить на ньому «Схвалити» і потім рев'юїтиме код. Прочитавши його,
вона має повністю бути в контексті задачі: що змінюється, де, як іде запит і чому саме так.
Пиши простою мовою, коротко, але конкретно: справжні назви репозиторіїв, пакетів, файлів,
класів/функцій, ендпоінтів, таблиць. Без води і загальних фраз. Код — лише сигнатури,
структури даних або псевдокод, де це допомагає зрозуміти; повних реалізацій не пиши.

## Як працювати
1. Уважно прочитай задачу. Дослідж код усіх репозиторіїв зі списку: як влаштовано те, що
   зачіпає задача, які там правила (CLAUDE.md, README, існуючі шаблони і стиль).
2. НІЧОГО не змінюй: не редагуй і не створюй файли, нічого не коміть. Твій результат — лише план.
3. Якщо в задачі щось неоднозначно — обери найрозумніший варіант і запиши припущення
   в розділ «Ризики та відкриті питання», щоб людина могла його виправити.

## Структура плану (markdown, саме ці розділи і в такому порядку)
# План: <коротка назва задачі>

## Коротко
2–4 речення: що робимо і навіщо, який результат побачить користувач або система.

## Як це працює зараз
Коротко про наявний код, який зачіпає задача: де він лежить і що робить.

## Що зміниться
По кожному репозиторію, який треба змінити (підзаголовок — назва репозиторію):
- **Нові сервіси / модулі / класи** — назва, де лежатиме, за що відповідає, від чого залежить.
- **Зміни в наявному коді** — що саме і навіщо.
- **Дані** — міграції, нові таблиці/колонки, зміни контрактів API, повідомлень, конфігів.
Репозиторії, які задача не зачіпає, не перелічуй.

## Флоу запиту
Як запит (HTTP, подія з черги, cron, виклик з іншого сервісу…) доходить до нового коду
і куди йде далі. Покажи схемою в блоці ` + "```text" + ` (стрілками: хто → кого → з якими даними),
а під нею — нумеровані кроки з назвами реальних функцій/ендпоінтів і тим, що відбувається
на кожному кроці, включно з помилками. Якщо флоу кілька (наприклад, запис і читання) — кожен окремо.

## Алгоритм
Лише якщо задача вимагає реалізувати алгоритм або нетривіальну логіку (розрахунки, розподіл,
пошук, стан-машина, ретраї…): вхід, вихід, кроки псевдокодом, граничні випадки, складність.
Якщо такого немає — напиши одне речення «Нетривіального алгоритму немає».

## Integration-тести (обов'язково)
Набір integration-тестів, які перевіряють фічу крізь справжні шари (API → сервіс → БД/черга…).
Прочитавши лише цей розділ, людина має зрозуміти, як працює фіча і який у неї флоу.
Для кожного тесту:
- **Назва** — як вона буде в коді, і в якому репозиторії та файлі лежатиме тест.
- **Сценарій** — Дано / Коли / Тоді: початковий стан, дія, очікуваний результат
  (відповідь, записи в БД, повідомлення, побічні ефекти).
- **Що перевіряє у флоу** — який крок(и) з розділу «Флоу запиту» покриває.
Покрий основний успішний сценарій, помилки і граничні випадки. Використовуй інструменти
integration-тестів, які вже є в репозиторії (якщо їх немає — напиши, що і як додати).

## Unit-тести
Коротко: що покрити unit-тестами (за потреби).

## Порядок реалізації
Нумеровані кроки для розробника в тому порядку, в якому їх зручно робити.

## Ризики та відкриті питання
Що може піти не так, що зачіпає інші частини системи, які припущення ти зробив.

## Формат відповіді (обов'язково)
Уся твоя фінальна відповідь — це план, одним повідомленням, починаючи з рядка «# План: …».
Нічого не пиши до нього і після нього.
`)
	return b.String()
}

func developerPrompt(t *storage.Task) string {
	var b strings.Builder
	b.WriteString("Ти — Developer. Реалізуй задачу нижче.\n\n")
	b.WriteString(taskHeader(t))
	b.WriteString(workspaceSection(t))
	b.WriteString(planSection(t))

	switch {
	case t.LastFeedback != "" && t.ReviewAttempts > 0:
		fmt.Fprintf(&b, "\n## Зауваження рев'юера, які треба виправити (ітерація %d з %d)\n%s\n",
			t.ReviewAttempts, t.MaxReviewAttempts, t.LastFeedback)
	case t.LastFeedback != "":
		// Задачу перезапустили після падіння (resume) — показуємо, що пішло не так.
		fmt.Fprintf(&b, "\n## Проблеми попереднього запуску, які треба виправити\n%s\n", t.LastFeedback)
	}

	fmt.Fprintf(&b, `
## Правила
- Усі репозиторії вже на гілці %s. Не перемикай гілки і не роби git push.
- Задача може потребувати змін у кількох репозиторіях — вноси їх у відповідних
  папках зі списку вище. Репозиторіїв поза цим списком не чіпай.
- Дотримуйся правил кожного репозиторію, який змінюєш (CLAUDE.md, .cursorrules, існуючий стиль коду).
- Напиши або онови юніт-тести для своїх змін.%s
- Оркестратор сам закомітить зміни в усіх репозиторіях після твоєї роботи.
`, t.BranchName, developerPlanRules(t))
	b.WriteString(briefSection(
		"що зроблено: що тепер працює інакше (з погляду користувача або системи);",
		"у яких репозиторіях зміни;",
		"які тести додано або оновлено;",
		"якщо відступив від плану — де і чому;",
		"якщо були зауваження рев'юера або QA — що з них виправлено.",
	))
	return b.String()
}

// developerPlanRules — правила для Developer'а, якщо є затверджений план.
func developerPlanRules(t *storage.Task) string {
	if strings.TrimSpace(t.Plan) == "" {
		return ""
	}
	return `
- Реалізуй задачу за затвердженим планом вище. Відступай від нього, лише якщо план
  у чомусь помиляється, — і поясни це в підсумку.
- Напиши ВСІ integration-тести з розділу плану «Integration-тести» (назви і сценарії —
  як у плані) і переконайся, що вони проходять.`
}

func reviewerPrompt(t *storage.Task, changed []storage.TaskRepo) string {
	return fmt.Sprintf(`Ти — Reviewer. Зроби код-рев'ю змін для задачі нижче.

%s%s%s
## Як дивитися зміни
%s- Перевір відповідність вимогам задачі%s, коректність, тести, стиль, безпеку.
- Якщо зміни в кількох репозиторіях — перевір, що вони узгоджені між собою
  (контракти API, міграції, конфіги).
- Якщо задача вимагала змін у репозиторії, якого немає серед змінених, — це зауваження.
- НЕ змінюй код сам.
%s
## Формат відповіді (обов'язково)
- Розділ «%s» — перед останнім рядком.
- Якщо все добре — останнім рядком напиши рівно: %s %s
- Якщо є зауваження — запиши їх конкретним списком (з назвою репозиторію біля кожного)
  у файл %s у поточній папці, а останнім рядком напиши рівно: %s %s
`, taskHeader(t), workspaceSection(t), planSection(t), diffSection(changed), reviewerPlanRules(t),
		briefSection(
			"що саме перевірено;",
			"висновок: чи можна приймати зміни і чому;",
			"головні зауваження, якщо є, — по одному рядку на кожне.",
		), briefHeading,
		verdictPrefix, verdictApprove,
		reviewFeedbackFile, verdictPrefix, verdictChanges)
}

func reviewerPlanRules(t *storage.Task) string {
	if strings.TrimSpace(t.Plan) == "" {
		return ""
	}
	return " і затвердженому плану (невиправдані відступи від плану — зауваження;\n" +
		"  відсутні integration-тести з плану — теж зауваження)"
}

func testerPrompt(t *storage.Task, changed []storage.TaskRepo) string {
	return fmt.Sprintf(`Ти — QA-інженер. Перевір, що задача нижче реалізована і працює.

%s%s%s
%s%s
## Що зробити
1. У кожному зміненому репозиторії знайди і запусти локальні тести (unit/integration).%s
2. Якщо в проєкті є веб-інтерфейс і тобі доступний Browser MCP (Playwright) —
   запусти застосунок і перевір UI-сценарії з опису задачі в headless-режимі.
3. Можеш дописати відсутні тести, але не змінюй бізнес-логіку.
%s
## Формат відповіді (обов'язково)
Коротко опиши, що перевірено і з яким результатом, додай розділ «%s», а останнім рядком напиши рівно:
%s %s — якщо все працює, або %s %s — якщо є проблеми.
`, taskHeader(t), workspaceSection(t), planSection(t), diffSection(changed), testInstructionsSection(t), testerPlanRules(t),
		briefSection(
			"які автотести запускав і з яким результатом (скільки пройшло / впало);",
			"які сценарії перевіряв вручну або в браузері;",
			"які тести дописав, якщо дописував;",
			"що не працює, якщо є проблеми.",
		), briefHeading,
		testPrefix, testPassed, testPrefix, testFailed)
}

// testInstructionsSection — інструкції людини, коли тестування перезапустили вручну.
func testInstructionsSection(t *storage.Task) string {
	if t.TestInstructions == "" {
		return ""
	}
	return "\n## Додаткові інструкції від людини (пріоритетні)\n" +
		"Тестування перезапустили вручну. Обов'язково виконай ці інструкції; якщо вони суперечать\n" +
		"загальним правилам нижче — слухайся їх. У звіті окремо напиши, що зроблено за кожною.\n\n" +
		t.TestInstructions + "\n"
}

func testerPlanRules(t *storage.Task) string {
	if strings.TrimSpace(t.Plan) == "" {
		return ""
	}
	return "\n   Обов'язково запусти integration-тести з розділу плану «Integration-тести» і для кожного\n" +
		"   напиши, чи він є в коді і чи проходить. Відсутній або червоний тест з плану — це FAILED."
}

// extractPlan дістає план з відповіді архітектора: від заголовка «# План»
// до кінця. Якщо заголовка немає — беремо всю відповідь.
func extractPlan(output string) string {
	lines := strings.Split(output, "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "# План") {
			return strings.TrimSpace(strings.Join(lines[i:], "\n"))
		}
	}
	return strings.TrimSpace(output)
}

// isApproved перевіряє, чи рев'юер схвалив зміни.
func isApproved(output string) bool {
	verdict := findMarker(output, verdictPrefix)
	if verdict != "" {
		return verdict == verdictApprove
	}
	// Запасний варіант зі специфікації: агент просто написав "APPROVE" останнім рядком.
	return lastLine(output) == verdictApprove
}

// isTestPassed перевіряє, чи тестувальник підтвердив успіх.
func isTestPassed(output string) bool {
	return findMarker(output, testPrefix) == testPassed
}

// findMarker шукає ОСТАННІЙ рядок, що починається з prefix, і повертає
// те, що після нього. Наприклад, для "VERDICT: APPROVE" і prefix "VERDICT:"
// поверне "APPROVE". Шукаємо з кінця, бо підсумок пишуть наприкінці.
func findMarker(output, prefix string) string {
	lines := strings.Split(output, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		// Прибираємо пробіли та markdown-прикраси типу **VERDICT: APPROVE**.
		line := strings.Trim(strings.TrimSpace(lines[i]), "*`_ ")
		if value, found := strings.CutPrefix(line, prefix); found {
			return strings.ToUpper(strings.Trim(value, "*`_ ."))
		}
	}
	return ""
}

// extractBrief вирізає з відповіді агента розділ «Підсумок простою мовою»:
// усе від заголовка до наступного заголовка, маркера вердикту або кінця.
// Якщо агент розділ не написав — повертає "" (адмінка покаже повну відповідь).
func extractBrief(output string) string {
	lines := strings.Split(output, "\n")
	start := -1
	// Шукаємо з кінця: підсумок — наприкінці, а назва розділу могла трапитися й раніше.
	for i := len(lines) - 1; i >= 0; i-- {
		heading := strings.Trim(strings.TrimSpace(lines[i]), "#*_: ")
		if strings.EqualFold(heading, briefHeading) {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return ""
	}

	end := len(lines)
	for i := start; i < len(lines); i++ {
		line := strings.Trim(strings.TrimSpace(lines[i]), "*`_ ")
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, verdictPrefix) || strings.HasPrefix(line, testPrefix) {
			end = i
			break
		}
	}
	return strings.TrimSpace(strings.Join(lines[start:end], "\n"))
}

func lastLine(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	return strings.Trim(strings.TrimSpace(lines[len(lines)-1]), "*`_ .")
}

// returnPrompt — передмова до завдання, коли задача повертається до ролі і ми
// продовжуємо її попередню сесію (claude --resume): агент пам'ятає свою минулу
// роботу над задачею, але завдання в нього нове.
const returnPrompt = `Задача знову повернулась до тебе, вже на новому етапі. Ти працював над нею раніше
в цій самій сесії — користуйся тим, що вже знаєш про задачу і код, але пам'ятай, що після тебе
код і обставини могли змінитися: перевір актуальний стан, перш ніж спиратися на пам'ять.
Нижче — нове завдання. Виконай саме його і дай відповідь у форматі, якого воно вимагає.

---

`

// resumePrompt — повідомлення агенту, коли відновлюємо його сесію після зупинки.
// Попереднє завдання і вся розмова вже є в сесії, тож повторювати їх не треба.
const resumePrompt = `Твою роботу було зупинено оператором посеред етапу, зараз її відновлено.
Останній виклик інструмента міг обірватися — спершу перевір поточний стан файлів
(git status, git diff), а потім продовж початкове завдання з того місця, де зупинився.
Доведи його до кінця і дай фінальну відповідь у форматі, якого вимагало початкове завдання.`
