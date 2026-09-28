package plan

import (
	"fmt"
	"regexp"
	"strings"

	"carriersim/internal/tree"
)

type SIM struct {
	Slot    string `json:"slot"`
	PLMN    string `json:"plmn"`
	IMSI    string `json:"imsi,omitempty"`
	Bundle  string `json:"bundle"`
	Current string `json:"current,omitempty"` // активный CFBundleIdentifier с телефона
}

var (
	reMCC  = regexp.MustCompile(`^\d{3}$`)
	reMNC  = regexp.MustCompile(`^\d{2,3}$`)
	reIMSI = regexp.MustCompile(`^\d{15}$`)
)

func SelectSIMs(rows []map[string]interface{}, bundle string) ([]SIM, error) {
	if bundle == "" {
		bundle = tree.Bundle
	}
	var selected []SIM
	seenSlots := map[string]bool{}
	seenIMSI := map[string]bool{}
	for _, row := range rows {
		mcc := fmt.Sprint(row["MCC"])
		mnc := fmt.Sprint(row["MNC"])
		if mcc == "<nil>" {
			mcc = ""
		}
		if mnc == "<nil>" {
			mnc = ""
		}
		slot, _ := row["Slot"].(string)
		imsi, _ := row["InternationalMobileSubscriberIdentity"].(string)
		if slot != "kOne" && slot != "kTwo" || seenSlots[slot] {
			return nil, fmt.Errorf("Неоднозначные слоты SIM; запись отменена.")
		}
		if !reMCC.MatchString(mcc) || !reMNC.MatchString(mnc) || !reIMSI.MatchString(imsi) || !hasPrefix(imsi, mcc+mnc) {
			return nil, fmt.Errorf("iPhone не сообщил полный IMSI для SIM %s%s. Включите линию и разблокируйте телефон.", mcc, mnc)
		}
		if seenIMSI[imsi] {
			return nil, fmt.Errorf("Один IMSI указан в двух слотах; запись отменена.")
		}
		seenSlots[slot] = true
		seenIMSI[imsi] = true
		current, _ := row["CFBundleIdentifier"].(string)
		selected = append(selected, SIM{
			Slot: slot, PLMN: mcc + mnc, IMSI: imsi, Bundle: bundle, Current: current,
		})
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("Телефон не сообщил ни одной SIM с доступным IMSI.")
	}
	return selected, nil
}

// ParseSlots accepts "", "all", "1", "2", "kOne", "kTwo", "1,2".
// Empty / all → apply to every detected SIM.
func ParseSlots(raw string) (map[string]bool, error) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" || raw == "all" || raw == "both" || raw == "обе" {
		return nil, nil // nil = all
	}
	out := map[string]bool{}
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		switch p {
		case "1", "sim1", "sim 1", "kone", "one":
			out["kOne"] = true
		case "2", "sim2", "sim 2", "ktwo", "two":
			out["kTwo"] = true
		default:
			return nil, fmt.Errorf("Неизвестный слот %q. Используйте: 1, 2 или all", p)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("Не выбран ни один слот SIM")
	}
	return out, nil
}

func FilterSIMs(all []SIM, slots map[string]bool) ([]SIM, error) {
	if slots == nil {
		return all, nil
	}
	var out []SIM
	for _, s := range all {
		if slots[s.Slot] {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("Выбранная SIM не найдена на телефоне")
	}
	return out, nil
}

func SlotLabel(slot string) string {
	switch slot {
	case "kOne":
		return "SIM 1"
	case "kTwo":
		return "SIM 2"
	default:
		return slot
	}
}

func hasPrefix(s, p string) bool {
	return len(s) >= len(p) && s[:len(p)] == p
}

func MakePlan(original tree.Tree, sims []SIM, targets tree.Tree) (tree.Tree, error) {
	desired := make(tree.Tree, len(original))
	for k, v := range original {
		desired[k] = v
	}
	for _, sim := range sims {
		n := sim.IMSI
		if existing, ok := original[n]; ok && existing.Kind != "l" {
			return nil, fmt.Errorf("Вместо ссылки IMSI обнаружен файл или каталог.")
		}
		// Явно снимаем старую IMSI-ссылку (если была) и ставим новую на целевой бандл.
		delete(desired, n)
		desired[n] = targets[sim.Bundle]
	}
	if err := tree.Validate(desired); err != nil {
		return nil, err
	}
	return desired, nil
}

// RemoveIMSILinks deletes root-level 15-digit IMSI symlinks.
// If sims is non-empty, only those IMSIs are removed; otherwise all IMSI links go.
func RemoveIMSILinks(original tree.Tree, sims []SIM) (tree.Tree, int, error) {
	only := map[string]bool{}
	for _, s := range sims {
		if s.IMSI != "" {
			only[s.IMSI] = true
		}
	}
	limit := len(only) > 0
	reIMSIName := regexp.MustCompile(`^\d{15}$`)
	desired := make(tree.Tree)
	removed := 0
	for name, node := range original {
		if node.Kind == "l" && reIMSIName.MatchString(name) {
			if !limit || only[name] {
				removed++
				continue
			}
		}
		desired[name] = node
	}
	if err := tree.Validate(desired); err != nil {
		return nil, 0, err
	}
	return desired, removed, nil
}

var Models = map[string]struct {
	Name   string
	Boards []string
}{
	"iPhone14,7": {Name: "iPhone 14", Boards: []string{"D27AP"}},
	"iPhone14,8": {Name: "iPhone 14 Plus", Boards: []string{"D28AP"}},
	"iPhone15,2": {Name: "iPhone 14 Pro", Boards: []string{"D73AP"}},
	"iPhone15,3": {Name: "iPhone 14 Pro Max", Boards: []string{"D74AP"}},
	"iPhone15,4": {Name: "iPhone 15", Boards: []string{"D37AP"}},
	"iPhone15,5": {Name: "iPhone 15 Plus", Boards: []string{"D38AP"}},
	"iPhone16,1": {Name: "iPhone 15 Pro", Boards: []string{"D83AP"}},
	"iPhone16,2": {Name: "iPhone 15 Pro Max", Boards: []string{"D84AP"}},
	"iPhone17,4": {Name: "iPhone 16 Plus", Boards: []string{"D48AP"}},
	"iPhone17,2": {Name: "iPhone 16 Pro Max", Boards: []string{"D94AP"}},
	"iPhone17,3": {Name: "iPhone 16", Boards: []string{"D47AP"}},
	"iPhone17,1": {Name: "iPhone 16 Pro", Boards: []string{"D93AP"}},
	"iPhone17,5": {Name: "iPhone 16e", Boards: []string{"V59AP"}},
	"iPhone18,1": {Name: "iPhone 17 Pro", Boards: []string{"V53AP"}},
	"iPhone18,2": {Name: "iPhone 17 Pro Max", Boards: []string{"V54AP"}},
	"iPhone18,4": {Name: "iPhone Air", Boards: []string{"D23AP"}},
	"iPhone18,3": {Name: "iPhone 17", Boards: []string{"V57AP"}},
	"iPhone18,5": {Name: "iPhone 17e", Boards: []string{"V159AP"}},
	"iPhone19,7": {Name: "iPhone 18 Pro Max", Boards: []string{"V64SAP"}},
	"iPhone19,3": {Name: "iPhone 18 Pro Max (U.S.)", Boards: []string{"V64AP"}},
	"iPhone19,2": {Name: "iPhone 18 Pro", Boards: []string{"V63AP"}},
}

func CheckPhone(info map[string]string) error {
	model, ok := Models[info["ProductType"]]
	hw := stringsToUpper(info["HardwareModel"])
	boardOK := false
	if ok {
		for _, b := range model.Boards {
			if hw == b {
				boardOK = true
				break
			}
		}
	}
	if !ok || !boardOK || info["ProductVersion"] != "27.0" ||
		(info["BuildVersion"] != "24A435" && info["BuildVersion"] != "24A437") {
		fmt.Println("Предупреждение: модель, плата или версия iOS не проверена. " +
			"Скрипт МОЖЕТ не работать. Продолжаю без ограничения совместимости.")
	}
	if info["ActivationState"] != "Activated" {
		return fmt.Errorf("iPhone не активирован.")
	}
	return nil
}

func stringsToUpper(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' {
			c -= 32
		}
		b[i] = c
	}
	return string(b)
}

func ModelName(productType string) string {
	if m, ok := Models[productType]; ok {
		return m.Name
	}
	return productType
}
