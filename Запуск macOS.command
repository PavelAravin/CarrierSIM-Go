#!/bin/bash
cd -- "$(dirname -- "$0")" || exit 1
if [ -x "./carriersim" ]; then
    ./carriersim "$@"
    carrier_status=$?
elif [ -x "./carriersim.exe" ]; then
    ./carriersim.exe "$@"
    carrier_status=$?
else
    echo 'Не найден carriersim. Соберите: go build -o carriersim ./cmd/carriersim'
    carrier_status=1
fi
if [ "$carrier_status" -ne 0 ] && [ "$#" -eq 0 ] && [ -t 0 ]; then
    read -r -p 'Нажмите Enter, чтобы закрыть окно…' carrier_reply
fi
exit "$carrier_status"
