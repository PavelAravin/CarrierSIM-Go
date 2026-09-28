package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"carriersim/internal/afcops"
	"carriersim/internal/assets"
	"carriersim/internal/device"
	"carriersim/internal/host"
	"carriersim/internal/plan"
	"carriersim/internal/run"
	"carriersim/internal/tree"
	"carriersim/internal/trigger"
	"carriersim/internal/ui"

	"github.com/danielpaulus/go-ios/ios"
	"github.com/google/uuid"
)

func rootDir() string {
	exe, err := os.Executable()
	if err != nil {
		wd, _ := os.Getwd()
		return wd
	}
	return filepath.Dir(exe)
}

func main() {
	os.Exit(runMain())
}

func runMain() int {
	if len(os.Args) > 1 && os.Args[1] == "--_host" {
		return runHostMode()
	}

	fmt.Println(ui.Dim + "Исследование — Vladimir B / vlw · порт на Go — PavelAravin" + ui.Reset)

	fs := flag.NewFlagSet("carriersim", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	check := fs.Bool("check", false, "проверить файлы и библиотеки Apple")
	status := fs.Bool("status", false, "показать активный профиль SIM без записи")
	restore := fs.Bool("restore", false, "удалить IMSI-ссылки (штатный выбор профиля)")
	triggerPath := fs.String("trigger", "", "свой IPCC вместо комплектного")
	bundleFlag := fs.String("bundle", "", "системный бандл (по умолчанию Vodafone_hu.bundle)")
	simFlag := fs.String("sim", "all", "какие SIM: 1, 2, all или ask")
	attempts := fs.Int("attempts", 3, "попытки при сбое связи")
	waitSeconds := fs.Int("wait-seconds", 180, "ожидание подключения")
	udid := fs.String("udid", "", "UDID при нескольких iPhone")
	runs := fs.String("runs", "", "каталог журналов")
	var appleDirs multiFlag
	fs.Var(&appleDirs, "apple-dir", "Windows: папка DLL Apple")

	args := os.Args[1:]
	if len(args) == 0 {
		return interactiveMenu(appleDirs)
	}
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *check && (*status || *restore) || *status && *restore {
		ui.Err("Укажите только один режим: --check, --status или --restore.")
		return 1
	}
	return executeCLI(*check, *status, *restore, *triggerPath, *bundleFlag, *simFlag, *attempts, *waitSeconds, *udid, *runs, appleDirs)
}

type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

func interactiveMenu(appleDirs []string) int {
	for {
		ui.Banner()
		ui.Menu()
		ui.Prompt("Ваш выбор")
		var choice string
		fmt.Scanln(&choice)
		switch strings.TrimSpace(choice) {
		case "0":
			ui.Info("Выход.")
			return 0
		case "1":
			code := executeCLI(false, false, false, "", "", "ask", 3, 180, "", "", appleDirs)
			if code != 0 {
				ui.Err("Действие не завершено. См. сообщение выше.")
			}
		case "2":
			ui.Section("Свой бандл")
			ui.Info("Бандл должен уже быть в системе iPhone.")
			ui.Info("Примеры: Swisscom_ch.bundle  MegaFon_ru.bundle")
			ui.Prompt("Имя бандла")
			var name string
			fmt.Scanln(&name)
			bundle, err := tree.NormalizeBundle(name)
			if err != nil {
				ui.Err(err.Error())
			} else {
				ui.Ok("Бандл: " + bundle)
				code := executeCLI(false, false, false, "", bundle, "ask", 3, 180, "", "", appleDirs)
				if code != 0 {
					ui.Err("Действие не завершено. См. сообщение выше.")
				}
			}
		case "3":
			code := executeCLI(false, false, true, "", "", "ask", 3, 180, "", "", appleDirs)
			if code != 0 {
				ui.Err("Действие не завершено. См. сообщение выше.")
			}
		case "4":
			_ = executeCLI(false, true, false, "", "", "all", 3, 180, "", "", appleDirs)
		case "5":
			_ = executeCLI(true, false, false, "", "", "all", 3, 180, "", "", appleDirs)
		case "6":
			help := filepath.Join(rootDir(), "README.txt")
			data, err := os.ReadFile(help)
			if err != nil {
				ui.Err(err.Error())
			} else {
				fmt.Println(string(data))
			}
		default:
			ui.Warn("Введите число от 0 до 6.")
			continue
		}
		fmt.Println()
		ui.Prompt("Enter — в меню")
		fmt.Scanln()
	}
}

// pickSIMsInteractive asks y/n for each detected SIM after the phone is read.
func pickSIMsInteractive(all []plan.SIM, actionLabel, yesHint, noHint, confirmMsg string) ([]plan.SIM, error) {
	ui.Section("Найденные SIM")
	for i, s := range all {
		cur := s.Current
		if cur == "" {
			cur = "?"
		}
		fmt.Printf("  %s%d%s  %s  ·  PLMN %s  ·  сейчас %s\n",
			ui.Cyan+ui.Bold, i+1, ui.Reset, plan.SlotLabel(s.Slot), s.PLMN, cur)
	}
	ui.Info(actionLabel)
	fmt.Println()
	fmt.Printf("  %s%s%s\n", ui.Yellow+ui.Bold, yesHint+"   ·   "+noHint, ui.Reset)
	fmt.Println()

	var chosen []plan.SIM
	for _, s := range all {
		ui.Prompt(fmt.Sprintf("%s для %s (PLMN %s)?", confirmMsg, plan.SlotLabel(s.Slot), s.PLMN))
		var ans string
		fmt.Scanln(&ans)
		ans = strings.ToLower(strings.TrimSpace(ans))
		if ans == "" || ans == "y" || ans == "yes" || ans == "д" || ans == "да" {
			chosen = append(chosen, s)
			ui.Ok(plan.SlotLabel(s.Slot) + " — выбрано")
		} else {
			ui.Info(plan.SlotLabel(s.Slot) + " — пропуск")
		}
	}
	if len(chosen) == 0 {
		return nil, fmt.Errorf("Не выбрана ни одна SIM — операция отменена")
	}
	return chosen, nil
}

func runHostMode() int {
	if len(os.Args) < 3 {
		host.Framed(map[string]interface{}{"ok": false, "error": "missing config"})
		return 1
	}
	if os.Args[2] == "check" {
		var cfg struct {
			Directories []string `json:"directories"`
		}
		_ = json.NewDecoder(os.Stdin).Decode(&cfg)
		if err := host.NativeHost("", nil, cfg.Directories); err != nil {
			host.Framed(map[string]interface{}{"ok": false, "error": err.Error()})
			return 1
		}
		return 0
	}
	if err := host.RunHostFromConfig(os.Args[2]); err != nil {
		host.Framed(map[string]interface{}{"ok": false, "error": err.Error()})
		return 1
	}
	return 0
}

func executeCLI(check, statusOnly, restore bool, triggerPath, bundleName, simSel string, attempts, waitSeconds int, udid, runsPath string, appleDirs []string) int {
	if attempts < 1 || attempts > 10 {
		ui.Err("Число попыток должно быть от 1 до 10.")
		return 1
	}
	if waitSeconds < 0 || waitSeconds > 3600 {
		ui.Err("Ожидание должно быть от 0 до 3600 секунд.")
		return 1
	}
	bundle, err := tree.NormalizeBundle(bundleName)
	if err != nil {
		ui.Err(err.Error())
		return 1
	}
	root := rootDir()
	bundles, assetTree, err := assets.Load(root, bundle)
	if err != nil {
		ui.Err(err.Error())
		return 1
	}
	exe, err := os.Executable()
	if err != nil {
		ui.Err(err.Error())
		return 1
	}
	if err := probeApple(exe, appleDirs); err != nil {
		if err2 := host.CheckLibraries(appleDirs); err2 != nil {
			ui.Err("Библиотеки Apple недоступны: " + err2.Error())
			return 1
		}
	}
	if check {
		ui.Ok("Триггеры целы, библиотеки Apple доступны. К телефону не подключались.")
		return 0
	}
	if runsPath == "" {
		runsPath = filepath.Join(root, "runs")
	}
	runsPath, _ = filepath.Abs(runsPath)
	ui.Info("Разблокируйте iPhone и подтвердите доверие. Закройте синхронизацию iTunes/Finder.")

	chosen, err := device.ChooseDevice(udid, waitSeconds)
	if err != nil {
		ui.Err(err.Error())
		return 1
	}

	// Resolved once so retries don't re-ask which SIMs to use.
	var resolvedSlots map[string]bool
	askDone := false
	if simSel != "ask" {
		resolvedSlots, err = plan.ParseSlots(simSel)
		if err != nil {
			ui.Err(err.Error())
			return 1
		}
		askDone = true
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		fmt.Printf("  %sПопытка %d/%d%s\n", ui.Dim, attempt, attempts, ui.Reset)
		code, err := execute(chosen, waitSeconds, statusOnly, restore, triggerPath, bundle, simSel, &resolvedSlots, &askDone, runsPath, bundles, assetTree, exe, appleDirs)
		if err == nil {
			return code
		}
		lastErr = err
		if !transient(err) || attempt == attempts {
			ui.Err(err.Error())
			return 1
		}
		ui.Warn("Временный сбой. Повтор…")
		time.Sleep(2 * time.Second)
	}
	ui.Err(lastErr.Error())
	return 1
}

func probeApple(exe string, dirs []string) error {
	raw, _ := json.Marshal(map[string]interface{}{"directories": dirs})
	cmd := newCmd(exe, "--_host", "check")
	cmd.Stdin = strings.NewReader(string(raw))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, out)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "CARRIER_SWAP_JSON:") {
			var row map[string]interface{}
			if json.Unmarshal([]byte(line[len("CARRIER_SWAP_JSON:"):]), &row) == nil {
				if ok, _ := row["ok"].(bool); ok {
					return nil
				}
				return fmt.Errorf("%v", row["error"])
			}
		}
	}
	return fmt.Errorf("no ok frame")
}

func transient(err error) bool {
	s := err.Error()
	for _, t := range []string{"Сбой AirTraffic", "Final source not consumed", "connection", "Connection", "timeout", "Timeout"} {
		if strings.Contains(s, t) {
			return true
		}
	}
	return false
}

func execute(udid string, waitSeconds int, statusOnly, restore bool, triggerPath, bundle, simSel string, resolvedSlots *map[string]bool, askDone *bool, runsPath string, bundles, assetTree tree.Tree, exe string, appleDirs []string) (int, error) {
	entry, err := device.ReadyDevice(udid, waitSeconds)
	if err != nil {
		return 1, err
	}
	info, err := device.GetInfo(entry)
	if err != nil {
		return 1, err
	}
	if err := plan.CheckPhone(device.InfoStrings(info)); err != nil {
		return 1, err
	}
	all, err := plan.SelectSIMs(info.RawCarriers, bundle)
	if err != nil {
		return 1, err
	}

	ui.PhoneHeader(plan.ModelName(info.ProductType), info.ProductVersion, info.BuildVersion)

	if statusOnly {
		ui.Section("Активный профиль на телефоне")
		for _, s := range all {
			ui.SIMStatus(plan.SlotLabel(s.Slot), s.PLMN, s.Current)
		}
		ui.Info("Это CFBundleIdentifier с телефона (не целевой бандл установки).")
		ui.Info("После смены профиля значение может обновиться после авиарежима.")
		ui.Info("Статус: запись не выполнялась.")
		return 0, nil
	}

	var sims []plan.SIM
	if simSel == "ask" {
		if !*askDone {
			var picked []plan.SIM
			if restore {
				picked, err = pickSIMsInteractive(all,
					"Сброс: удалить IMSI-ссылки → штатный выбор профиля",
					"y — да, сбросить",
					"n — нет, пропустить",
					"Сбросить подмену",
				)
			} else {
				picked, err = pickSIMsInteractive(all,
					"Профиль: "+bundle,
					"y — да, установить",
					"n — нет, пропустить",
					"Установить настройки",
				)
			}
			if err != nil {
				return 1, err
			}
			slots := map[string]bool{}
			for _, s := range picked {
				slots[s.Slot] = true
			}
			*resolvedSlots = slots
			*askDone = true
		}
		sims, err = plan.FilterSIMs(all, *resolvedSlots)
		if err != nil {
			return 1, err
		}
	} else {
		sims, err = plan.FilterSIMs(all, *resolvedSlots)
		if err != nil {
			return 1, err
		}
	}

	applySet := map[string]bool{}
	for _, s := range sims {
		applySet[s.Slot] = true
	}
	if restore {
		ui.Section("План сброса")
		for _, s := range all {
			target := "пропуск"
			if applySet[s.Slot] {
				target = "штатный профиль"
			}
			ui.SIMLine(plan.SlotLabel(s.Slot), s.PLMN, target, applySet[s.Slot])
		}
	} else {
		ui.Section("План установки")
		for _, s := range all {
			target := "пропуск"
			if applySet[s.Slot] {
				target = s.Bundle
			}
			ui.SIMLine(plan.SlotLabel(s.Slot), s.PLMN, target, applySet[s.Slot])
		}
	}
	ui.Info("Применение к: " + summarizeSlots(sims))

	runDir := filepath.Join(runsPath, time.Now().Format("20060102-150405-")+uuid.New().String()[:6])
	if err := os.MkdirAll(runsPath, 0o700); err != nil {
		return 1, err
	}
	if err := os.Mkdir(runDir, 0o700); err != nil {
		return 1, err
	}
	ui.Info("Журнал: " + runDir)
	ui.Warn("Не отключайте iPhone до конца операции.")
	devJSON := device.AsMap(info)
	devJSON["udid_hash"] = tree.Digest([]byte(udid))
	if restore {
		devJSON["action"] = "restore"
	} else {
		devJSON["target_bundle"] = bundle
	}
	_ = run.SaveJSON(filepath.Join(runDir, "device.json"), devJSON)

	plmns := map[string]struct{}{}
	for _, r := range info.RawCarriers {
		plmns[fmt.Sprint(r["MCC"])+fmt.Sprint(r["MNC"])] = struct{}{}
	}
	var triggerFile string
	if triggerPath != "" {
		raw, err := os.ReadFile(triggerPath)
		if err != nil {
			return 1, err
		}
		triggerFile = filepath.Join(runDir, "custom-trigger.ipcc")
		if err := os.WriteFile(triggerFile, raw, 0o600); err != nil {
			return 1, err
		}
		if _, err := trigger.CheckTrigger(triggerFile, plmns); err != nil {
			return 1, err
		}
		trigger.CheckHardware(triggerFile, info.HardwareModel)
	} else {
		for _, name := range []string{"AVEA_tr.ipcc", "Swisscom_ch.ipcc", "O2_Germany.ipcc"} {
			node, ok := assetTree["triggers/"+name]
			if !ok {
				continue
			}
			candidate := filepath.Join(runDir, name)
			if err := os.WriteFile(candidate, node.Data, 0o600); err != nil {
				return 1, err
			}
			if _, err := trigger.CheckTrigger(candidate, plmns); err != nil {
				_ = os.Remove(candidate)
				continue
			}
			trigger.CheckHardware(candidate, info.HardwareModel)
			triggerFile = candidate
			break
		}
	}
	if triggerFile == "" {
		return 1, fmt.Errorf("Не найден независимый триггер для этих SIM.")
	}

	initDir := filepath.Join(runDir, "initialize")
	_ = os.Mkdir(initDir, 0o700)
	ui.Step(1, 4, "Подготовка пересканирования…")
	if _, err := trigger.Install(entry, triggerFile, initDir); err != nil {
		_ = run.SaveJSON(filepath.Join(runDir, "error.json"), map[string]string{"error": err.Error()})
		return 1, err
	}
	ui.Step(2, 4, "Сохранение исходных настроек…")
	original, err := afcops.Transfer(afcops.TransferOpts{
		Entry: entry, RunDir: filepath.Join(runDir, "snapshot"),
		ExePath: exe, AppleDirs: appleDirs,
	})
	if err != nil {
		_ = run.SaveJSON(filepath.Join(runDir, "error.json"), map[string]string{"error": err.Error()})
		return 1, err
	}
	if original == nil {
		return 1, fmt.Errorf("Не удалось сохранить исходный каталог.")
	}
	info2, err := device.GetInfo(entry)
	if err != nil {
		return 1, err
	}
	currentAll, err := plan.SelectSIMs(info2.RawCarriers, bundle)
	if err != nil {
		return 1, err
	}
	current, err := plan.FilterSIMs(currentAll, *resolvedSlots)
	if err != nil {
		return 1, err
	}
	if !simsEqual(sims, current) {
		return 1, fmt.Errorf("SIM изменились во время операции; запись отменена.")
	}

	var desired tree.Tree
	if restore {
		// Если выбраны все найденные SIM — убираем все IMSI-ссылки (как в Python).
		// Иначе только IMSI выбранных SIM; старые ссылки других слотов не трогаем.
		removeAll := len(sims) == len(all)
		var removed int
		if removeAll {
			desired, removed, err = plan.RemoveIMSILinks(original, nil)
		} else {
			desired, removed, err = plan.RemoveIMSILinks(original, sims)
		}
		if err != nil {
			return 1, err
		}
		_ = run.SaveJSON(filepath.Join(runDir, "plan.json"), map[string]interface{}{
			"action": "remove-imsi", "removed": removed,
			"before": tree.Hash(original), "after": tree.Hash(desired),
		})
		ui.Step(3, 4, fmt.Sprintf("Удаление IMSI-ссылок: %d…", removed))
		if _, err := afcops.Transfer(afcops.TransferOpts{
			Entry: entry, RunDir: filepath.Join(runDir, "restore"), Payload: desired, Expected: original,
			ExePath: exe, AppleDirs: appleDirs,
		}); err != nil {
			_ = run.SaveJSON(filepath.Join(runDir, "error.json"), map[string]string{"error": err.Error()})
			return 1, err
		}
	} else {
		desired, err = plan.MakePlan(original, sims, bundles)
		if err != nil {
			return 1, err
		}
		slotRows := make([]map[string]interface{}, 0, len(sims))
		for _, s := range sims {
			slotRows = append(slotRows, map[string]interface{}{"slot": s.Slot, "plmn": s.PLMN, "bundle": s.Bundle})
		}
		_ = run.SaveJSON(filepath.Join(runDir, "plan.json"), map[string]interface{}{
			"slots": slotRows, "target_bundle": bundle, "before": tree.Hash(original), "after": tree.Hash(desired),
		})
		ui.Step(3, 4, "Запись ссылок по IMSI…")
		if _, err := afcops.Transfer(afcops.TransferOpts{
			Entry: entry, RunDir: filepath.Join(runDir, "apply"), Payload: desired, Expected: original,
			ExePath: exe, AppleDirs: appleDirs,
		}); err != nil {
			_ = run.SaveJSON(filepath.Join(runDir, "error.json"), map[string]string{"error": err.Error()})
			return 1, err
		}
	}
	readback, err := afcops.Transfer(afcops.TransferOpts{
		Entry: entry, RunDir: filepath.Join(runDir, "readback"),
		ExePath: exe, AppleDirs: appleDirs,
	})
	if err != nil {
		return 1, err
	}
	if !tree.Equal(readback, desired) {
		return 1, fmt.Errorf("Обратное чтение не совпало.")
	}
	ui.Step(4, 4, "Проверка подписей…")
	rescan := filepath.Join(runDir, "rescan")
	_ = os.Mkdir(rescan, 0o700)
	installation, err := trigger.Install(entry, triggerFile, rescan)
	if err != nil {
		return 1, err
	}
	result := trigger.ReportLog(filepath.Join(rescan, "commcenter.log"), sims)
	_ = run.SaveJSON(filepath.Join(runDir, "result.json"), map[string]interface{}{
		"catalog_verified": true, "installation": installation, "slots": result,
		"restore": restore, "target_bundle": bundle,
	})
	unconfirmed := false
	ui.Section("Результат")
	for _, s := range result {
		verified, _ := s["verified"].(bool)
		ok := verified && (restore || s["selected"] == s["expected"])
		unconfirmed = unconfirmed || !ok
		label := plan.SlotLabel(s["slot"].(string))
		if ok {
			sel := s["selected"]
			if sel == nil || sel == "" {
				ui.Ok(fmt.Sprintf("%s (%s): подпись принята", label, s["plmn"]))
			} else {
				ui.Ok(fmt.Sprintf("%s (%s): %s — подпись принята", label, s["plmn"], sel))
			}
		} else {
			ui.Warn(fmt.Sprintf("%s (%s): пакет не подтверждён; см. журнал", label, s["plmn"]))
		}
	}
	if restore {
		ui.Ok("IMSI-ссылки удалены (для выбранных SIM). Обычные ссылки операторов сохранены.")
	}
	if unconfirmed {
		return 2, nil
	}
	ui.Ok("Готово. Авиарежим ~15 секунд, затем проверьте связь.")
	return 0, nil
}

func summarizeSlots(sims []plan.SIM) string {
	parts := make([]string, 0, len(sims))
	for _, s := range sims {
		parts = append(parts, plan.SlotLabel(s.Slot))
	}
	return strings.Join(parts, ", ")
}

func simsEqual(a, b []plan.SIM) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Slot != b[i].Slot || a[i].PLMN != b[i].PLMN || a[i].IMSI != b[i].IMSI || a[i].Bundle != b[i].Bundle {
			return false
		}
	}
	return true
}

var _ = ios.ListDevices
