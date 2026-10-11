#!/usr/bin/env bash
set -euo pipefail
I18N_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
export I18N_ROOT
python3 - "$@" <<'PY'
"""Seed native dictionaries from meaning-identical website source messages.
Website files are read only. Existing authored translations always win.
"""
import json,pathlib,re,os
ROOT=pathlib.Path(os.environ['I18N_ROOT'])
LANGUAGES=['en','es','fr','de','pt','ja','zh-Hans','zh-Hant','uk','hi','it','ko','id','pl']
SINGLE={'ja','zh-Hans','zh-Hant','ko','id'}
SITE=pathlib.Path('/home/joeg/Projects/a-weather-site/site/assets/i18n')
english=json.loads((SITE/'en.json').read_text())
lookup={value:key for key,value in english.items() if isinstance(value,str)}
sources=json.loads((ROOT/'translations/sources.json').read_text())
for code in LANGUAGES[1:]:
    web=json.loads((SITE/(code+'.json')).read_text())
    path=ROOT/'translations/messages'/(code+'.json')
    output=json.loads(path.read_text()) if path.exists() else {}
    for source in sources:
        if source in output: continue
        if source in lookup: output[source]=web[lookup[source]]; continue
        trimmed=source.strip()
        if trimmed and trimmed in lookup:
            prefix=source[:len(source)-len(source.lstrip())]; suffix=source[len(source.rstrip()):]
            output[source]=prefix+web[lookup[trimmed]]+suffix
        if source=='%n minute(s) ago':
            forms=['other'] if code in SINGLE else ['one','few','many'] if code in {'uk','pl'} else ['one','other']
            output[source]=[web['ageMinutes'].get(form,web['ageMinutes']['other']).replace('{count}','%n') for form in forms]
        if source=='Feels like %1 · High %2 · Low %3': output[source]=web['feels'].replace('{feels}','%1').replace('{high}','%2').replace('{low}','%3')
    path.parent.mkdir(parents=True,exist_ok=True)
    path.write_text(json.dumps(dict(sorted(output.items())),ensure_ascii=False,indent=2)+'\n')

PY
