#!/usr/bin/env bash
set -euo pipefail
I18N_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
export I18N_ROOT
python3 - "$@" <<'PY'
"""Extract App-context sources and generate/validate offline Qt and Go catalogs.

Authored locale dictionaries are translations/messages/<code>.json; absent
translations remain unfinished Qt messages and are omitted from Go catalogs.
Never infer translation completeness from source-key count alone.
"""
import argparse, collections, json, os, pathlib, re, sys, xml.etree.ElementTree as ET
ROOT = pathlib.Path(os.environ['I18N_ROOT'])
LANGUAGES = ['en','es','fr','de','pt','ja','zh-Hans','zh-Hant','uk','hi','it','ko','id','pl']
SINGLE = {'ja','zh-Hans','zh-Hant','ko','id'}
STRING = r'"(?:\\.|[^"\\])*"'
PLACEHOLDERS = re.compile(r'%[1-9][0-9]*|%n|\{[a-zA-Z][a-zA-Z0-9_]*\}')
def literal(value): return json.loads(value)
def write_atomic(path, content):
    temporary = path.with_name(path.name+'.i18n-tmp')
    temporary.write_text(content)
    temporary.replace(path)
WIDGET_EXTRAS = {
    'The updater did not start. Check your connection and try again.',
    'Could not check for updates. Check your connection and try again.',
    'Update could not start. Check your connection and ensure the plugin has no local changes, then try again.',
}
def widget_sources(sources):
    return {source for source,locations in sources.items() if source in WIDGET_EXTRAS or any(location.startswith('quickshell/') or location == 'internal/app/bar.go' for location in locations)}
def extract():
    entries = collections.defaultdict(set)
    patterns = [re.compile(r'(?:I18n\.(?:t|source)|\bt)\(\s*('+STRING+r')'), re.compile(r'qsTranslate\(\s*"App"\s*,\s*('+STRING+r')'), re.compile(r'(?:QCoreApplication::translate|QT_TRANSLATE_NOOP)\(\s*"App"\s*,\s*('+STRING+r')'), re.compile(r'locale\.[TN]\(\s*[^,]+,\s*('+STRING+r')'), re.compile(r'\btr\(\s*('+STRING+r')')]
    paths = list((ROOT/'ui/qml').rglob('*.qml')) + list((ROOT/'ui/qml').rglob('*.js')) + list((ROOT/'native/qt').glob('*.cpp')) + list((ROOT/'internal').rglob('*.go')) + list((ROOT/'quickshell').rglob('*.qml')) + list((ROOT/'quickshell').rglob('*.js'))
    for path in paths:
        if path.name.endswith('_test.go'): continue
        text = path.read_text()
        scoped_patterns = patterns
        if str(path.relative_to(ROOT)).startswith('quickshell/'):
            text = re.sub(r'var dictionaries = \(.*?\);', '', text, flags=re.S)
            scoped_patterns = patterns + [re.compile(r'\btext\(\s*code\s*,\s*('+STRING+r')')]
        for pattern in scoped_patterns:
            for match in pattern.finditer(text):
                source = literal(match.group(1))
                if source: entries[source].add(str(path.relative_to(ROOT)))
    extra = ROOT/'translations/extra-sources.json'
    if extra.exists():
        for source in json.loads(extra.read_text()): entries[source].add('translations/extra-sources.json')
    return {source: sorted(files) for source,files in sorted(entries.items())}
def build(sources):
    directory = ROOT/'translations/messages'; directory.mkdir(parents=True,exist_ok=True)
    for code in LANGUAGES:
        path = directory/(code+'.json')
        authored = json.loads(path.read_text()) if path.exists() else {}
        if code == 'en': authored = {**{s: s for s in sources}, **authored}
        catalog = {}
        tree = ET.Element('TS',version='2.1',language=code,sourcelanguage='en')
        context=ET.SubElement(tree,'context'); ET.SubElement(context,'name').text='App'
        for source,locations in sources.items():
            plural='%n' in source
            message=ET.SubElement(context,'message', **({'numerus':'yes'} if plural else {}))
            for location in locations: ET.SubElement(message,'location',filename='../'+location)
            ET.SubElement(message,'source').text=source
            translated=authored.get(source)
            translation=ET.SubElement(message,'translation', **({'type':'unfinished'} if translated is None else {}))
            if translated is not None:
                catalog[source]=translated
                if isinstance(translated,list):
                    for form in translated: ET.SubElement(translation,'numerusform').text=form
                else: translation.text=translated
        ET.indent(tree,space='  ')
        write_atomic(ROOT/'translations'/('app_'+code+'.ts'), '<?xml version="1.0" encoding="utf-8"?>\n'+ET.tostring(tree,encoding='unicode')+'\n')
        write_atomic(ROOT/'internal/locale/catalogs'/(code+'.json'),json.dumps(catalog,ensure_ascii=False,indent=2)+'\n')
    widget = ROOT/'quickshell/a-weather-app.weather/Translations.js'
    if widget.exists():
        wanted = widget_sources(sources)
        dictionaries = {code: {source: value for source,value in json.loads((ROOT/'internal/locale/catalogs'/(code+'.json')).read_text()).items() if source in wanted} for code in LANGUAGES}
        content = widget.read_text()
        content = re.sub(r'var dictionaries = \(.*?\);', lambda _: 'var dictionaries = ('+json.dumps(dictionaries,ensure_ascii=False,separators=(',',':'))+');', content, count=1, flags=re.S)
        write_atomic(widget,content)
    desktop=ROOT/'packaging/a-weather-app.desktop'
    if desktop.exists():
        content=re.sub(r'^Comment\[[^]]+\]=.*\n?', '', desktop.read_text(), flags=re.M)
        match=re.search(r'^Comment=(.*)$',content,re.M)
        if match:
            source=match.group(1)
            lines=[]
            for code in LANGUAGES[1:]:
                translated=json.loads((ROOT/'internal/locale/catalogs'/(code+'.json')).read_text()).get(source)
                if isinstance(translated,str):
                    aliases = {'zh-Hans':['zh_Hans','zh_CN','zh_SG'], 'zh-Hant':['zh_Hant','zh_TW','zh_HK','zh_MO'], 'uk':['uk','ru']}
                    for desktop_locale in aliases.get(code,[code]): lines.append('Comment['+desktop_locale+']='+translated)
            content=content[:match.end()]+'\n'+'\n'.join(lines)+content[match.end():]
            write_atomic(desktop,content)
    (ROOT/'translations/sources.json').write_text(json.dumps(sources,ensure_ascii=False,indent=2)+'\n')
def validate(sources, complete=False):
    failures=[]; report={}
    for code in LANGUAGES:
        path=ROOT/'translations/messages'/(code+'.json')
        authored=json.loads(path.read_text()) if path.exists() else {}
        if code=='en': authored={**{s:s for s in sources},**authored}
        missing=sorted(set(sources)-set(authored)); extra=sorted(set(authored)-set(sources))
        unchanged=[]
        for source,value in authored.items():
            forms=value if isinstance(value,list) else [value]
            count=1 if code in SINGLE else 3 if code in {'uk','pl'} else 2
            if '%n' in source and (not isinstance(value,list) or len(forms)!=count): failures.append(f'{code}: wrong plural count: {source}')
            if '%n' not in source and not isinstance(value,str): failures.append(f'{code}: nonplural must be text: {source}')
            for form in forms:
                if not isinstance(form,str) or not form.strip(): failures.append(f'{code}: empty translation: {source}'); continue
                if collections.Counter(PLACEHOLDERS.findall(source))!=collections.Counter(PLACEHOLDERS.findall(form)): failures.append(f'{code}: placeholder mismatch: {source}')
            if value==source: unchanged.append(source)
            for form in forms:
                if isinstance(form,str):
                    for marker in ['© OpenStreetMap contributors (ODbL)', 'Open-Meteo', 'A Weather App', 'Hyprland', 'National Weather Service', 'SunCalc/Meeus', 'libnotify', 'ipwho.is', 'Omarchy', 'GeoNames', 'CAMS', 'NOAA', 'NWS', '.png']:
                        if source.count(marker) != form.count(marker): failures.append(f'{code}: protected name or attribution mismatch: {source}: {marker}')
        if complete and missing: failures.append(f'{code}: {len(missing)} missing translations')
        report[code]={'translated':len(set(sources)&set(authored)),'missing':missing,'obsolete':extra,'identical_source':unchanged}
    (ROOT/'translations/coverage.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
    for code,data in report.items(): print(f'{code}: {data["translated"]}/{len(sources)} translated; {len(data["missing"])} missing')
    for failure in failures: print(failure,file=sys.stderr)
    return not failures
if __name__=='__main__':
    parser=argparse.ArgumentParser(); parser.add_argument('--check',action='store_true'); parser.add_argument('--complete',action='store_true'); args=parser.parse_args()
    sources=extract()
    valid = validate(sources,args.complete)
    if valid and not args.check: build(sources)
    sys.exit(0 if valid else 1)

PY
