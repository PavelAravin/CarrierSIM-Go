package ui

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	Reset   = "\033[0m"
	Bold    = "\033[1m"
	Dim     = "\033[2m"
	Cyan    = "\033[36m"
	Green   = "\033[32m"
	Yellow  = "\033[33m"
	Magenta = "\033[35m"
	White   = "\033[37m"
	Blue    = "\033[34m"
)

func Banner() {
	fmt.Println()
	fmt.Println(Cyan + Bold + "  CarrierSIM" + Reset)
	fmt.Println(Dim + "  профиль оператора · привязка по IMSI" + Reset)
	fmt.Println(Dim + "  " + strings.Repeat("─", 42) + Reset)
	fmt.Println(Dim + "  Vladimir B / vlw  ·  Go: PavelAravin" + Reset)
	fmt.Println()
}

func Menu() {
	fmt.Println(Bold + "  Меню" + Reset)
	fmt.Println()
	item("1", "Установить Vodafone HU", "после чтения SIM")
	item("2", "Установить свой бандл", "после чтения SIM")
	item("3", "Сброс IMSI-ссылок", "штатный выбор профиля")
	item("4", "Статус SIM", "без записи")
	item("5", "Проверка ПК", "файлы и Apple")
	item("6", "Справка", "")
	fmt.Println()
	item("0", "Выход", "")
	fmt.Println()
}

func item(key, title, hint string) {
	fmt.Printf("  %s%s%s  %s%s%s", Cyan+Bold, key, Reset, Bold, title, Reset)
	if hint != "" {
		pad := 26 - utf8.RuneCountInString(title)
		if pad < 2 {
			pad = 2
		}
		fmt.Printf("%s%s%s%s", strings.Repeat(" ", pad), Dim, hint, Reset)
	}
	fmt.Println()
}

func Prompt(label string) {
	fmt.Printf("  %s›%s %s ", Cyan+Bold, Reset, label)
}

func Info(msg string) { fmt.Println(Dim + "  " + msg + Reset) }
func Ok(msg string)   { fmt.Println(Green + Bold + "  ✓ " + Reset + msg) }
func Warn(msg string) { fmt.Println(Yellow + "  ! " + Reset + msg) }
func Err(msg string)  { fmt.Println(Magenta + "  × " + Reset + msg) }
func Step(n, total int, msg string) {
	fmt.Printf("  %s[%d/%d]%s %s\n", Cyan+Bold, n, total, Reset, msg)
}

func PhoneHeader(model, iosVer, build string) {
	fmt.Println()
	fmt.Printf("  %s%s%s  ·  iOS %s (%s)\n", Bold, model, Reset, iosVer, build)
}

func SIMLine(label, plmn, bundle string, apply bool) {
	mark := Dim + "—" + Reset
	if apply {
		mark = Green + Bold + "→" + Reset
	}
	fmt.Printf("  %s%-5s%s  PLMN %-6s  %s  %s\n", Bold, label, Reset, plmn, mark, bundle)
}

func SIMStatus(label, plmn, current string) {
	if current == "" {
		current = "?"
	}
	fmt.Printf("  %s%-5s%s  PLMN %-6s  сейчас: %s%s%s\n", Bold, label, Reset, plmn, Cyan, current, Reset)
}

func Section(title string) {
	fmt.Println()
	fmt.Println(Bold + "  " + title + Reset)
	fmt.Println(Dim + "  " + strings.Repeat("─", 42) + Reset)
}
