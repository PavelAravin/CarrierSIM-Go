# CarrierSIM (Go)

Порт CarrierSIM на **Go**. Python и `.venv` не нужны: один бинарник `carriersim` / `carriersim.exe`.

Исходное исследование и логика — **Vladimir B / vlw**. Порт на Go — **PavelAravin**.

> Экспериментальный инструмент. Проверен на части конфигураций iPhone / iOS. На других моделях и версиях может не заработать.

## Что нужно

* **Go 1.22+** — только если собираете из исходников.
* **Windows:** iTunes x64 с сайта Apple (не Microsoft Store / Apple Devices).
* **macOS:** дополнительное ПО не требуется.
* iPhone, USB-кабель; закрытые Finder/iTunes во время операции.

## Сборка

```bash
go build -o carriersim.exe ./cmd/carriersim   # Windows
go build -o carriersim ./cmd/carriersim       # macOS
```

Рядом с бинарником должны лежать `assets.zip` и (желательно) `README.txt`.

## Запуск

1. Подключите iPhone, разблокируйте, нажмите «Доверять».
2. `Запуск Windows.cmd` / `Запуск macOS.command` или напрямую `carriersim.exe`.
3. В меню:

| Пункт | Действие |
| ----- | -------- |
| **1** | Установить Vodafone HU (выбор SIM после чтения телефона) |
| **2** | Установить свой системный бандл |
| **3** | Сброс IMSI-ссылок → штатный выбор профиля |
| **4** | Статус: активный `CFBundleIdentifier` с телефона |
| **5** | Проверка ПК (файлы и библиотеки Apple) |
| **6** | Справка |

## CLI

```text
carriersim.exe --bundle Swisscom_ch.bundle
carriersim.exe --bundle Swisscom_ch.bundle --sim 1
carriersim.exe --sim 2
carriersim.exe --restore --sim ask
carriersim.exe --status
carriersim.exe --check
```

`--sim`: `1` | `2` | `all` | `ask`.

Бандл должен уже существовать в системе iPhone (`/System/Library/Carrier Bundles/iPhone`). Меняются только пользовательские ссылки по IMSI.

## После установки

1. «Настройки → Сотовая связь → SIM» → «Вызовы по Wi-Fi».
2. Авиарежим ~15 с, затем Wi‑Fi.
3. Дождитесь отметки VoWiFi; авиарежим можно выключить.

Подробности, ограничения 5G/EVS и техническое описание — в `README.txt`.

## Как это работает (кратко)

Тот же подход, что у Python-версии: IPCC-триггер → снимок пользовательского каталога carrier → симлинки `IMSI → *.bundle` → проверка через CommCenter. Перенос каталога — метод AirLift / AirTrafficHost (см. `LICENSE-AirLift.txt`).

## Авторство

* Исследование, идея и исходная реализация — **Vladimir B / vlw** (`vlwwwwww@gmail.com`).
* Порт на Go — **PavelAravin**.
