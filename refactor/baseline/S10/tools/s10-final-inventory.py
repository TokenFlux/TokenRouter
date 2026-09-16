# S10 只读交付清单，文件/符号来自实际差异，功能归属与退出边界见 migration-ledger。
from pathlib import Path
import json,gzip,hashlib,subprocess,re,sys
sys.path.insert(0,'/tmp');from s08util import ast
root=Path('/Users/daodaoneko/GolandProjects/TokenRouter');out=root/'refactor/baseline/S10';base=json.loads(gzip.decompress((out/'initial-file-checksums.json.gz').read_bytes()))
paths=set(subprocess.check_output(['git','diff','--name-only'],cwd=root,text=True).splitlines())
paths.update(set(subprocess.check_output(['git','ls-files','--others','--exclude-standard'],cwd=root,text=True).splitlines())-set(base['untracked']))
rows=[]
for path in sorted(paths):
 if not path.startswith('backend/') or not path.endswith('.go'):continue
 p=root/path
 if not p.exists():rows.append({'path':path,'status':'deleted','original_sha256':base['tracked'].get(path)});continue
 s=p.read_text();declarations=ast(p)
 for d in declarations:
  d['line']=s.count('\n',0,d['start'])+1
 row={'path':path,'status':'modified' if path in base['tracked'] else 'new','sha256':hashlib.sha256(p.read_bytes()).hexdigest(),'original_sha256':base['tracked'].get(path),'package':re.search(r'^package\s+(\w+)',s,re.M).group(1),'generated':bool(re.search(r'^// Code generated .* DO NOT EDIT\.',s,re.M)),'build_constraints':re.findall(r'^//go:build (.+)$',s,re.M),'os_suffix':next((x for x in ['darwin','linux','windows','unix']if p.stem.endswith('_'+x)),None),'document_anchors':re.findall(r'@project-doc[^\n]+',s),'declarations':declarations}
 rows.append(row)
(out/'final-file-declarations.json.gz').write_bytes(gzip.compress(json.dumps(rows,ensure_ascii=False).encode()))
(out/'final-file-summary.json').write_text(json.dumps({'files':len(rows),'deleted':sum(x['status']=='deleted'for x in rows),'new':sum(x['status']=='new'for x in rows),'symbols':sum(len(x.get('declarations',[]))for x in rows),'source':'实际 git diff 与新增文件，排除最初其它任务内容；按符号的功能所有权见 migration-ledger，静态消费者另列。'},ensure_ascii=False,indent=2))
print((out/'final-file-summary.json').read_text())
