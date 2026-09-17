# 只记录当前真实文件、构建条件、引用和声明，不替代 depguard。
from pathlib import Path
import subprocess,json,gzip,re,hashlib,collections,sys
root=Path(__file__).resolve().parents[4]
backend=root/'backend'
changes=subprocess.check_output(['git','diff','--name-only','-z'],cwd=root,text=True).split('\0')
new=subprocess.check_output(['git','ls-files','--others','--exclude-standard','-z'],cwd=root,text=True).split('\0')
selected=sorted({x for x in changes+new if x.startswith('backend/') and x.endswith('.go')})
# import 段和单行 import 均包含；不把函数内字符串当成依赖。
def imports(source):
 blocks=re.findall(r'(?ms)^import\s*\(.*?^\)',source)+re.findall(r'(?m)^import\s+[^\n]+',source)
 return sorted(set(re.findall(r'"([^"\n]+)"','\n'.join(blocks))))
consumers=collections.defaultdict(list)
for path in backend.rglob('*.go'):
 for imp in imports(path.read_text()):consumers[imp].append(str(path.relative_to(root)))
rows=[]
for relative in selected:
 path=root/relative
 if not path.exists():rows.append({'source':relative,'status':'removed'});continue
 data=path.read_bytes();source=data.decode();pkg=re.search(r'^package\s+(\w+)',source,re.M).group(1)
 # AST 工具是本次临时清点程序，声明级差异仍由阶段迁移账本解释。
 declarations=json.loads(subprocess.check_output(['/tmp/s08-ast',str(path)]))[str(path)]
 role='compatibility/adapter'
 if '/internal/payment/'in relative or '/internal/promotion/'in relative:
  role='payment core'if '/internal/payment/'in relative else'promotion core'
  for part,label in [('/httpapi/','HTTP Adapter'),('/provider/','provider Adapter'),('/postgres/','PostgreSQL Adapter'),('/rediscache/','Redis Adapter')]:
   if part in relative:role=label;break
 elif '/internal/app/'in relative:role='app composition'
 package_path='github.com/TokenFlux/TokenRouter/'+str(path.parent.relative_to(backend))
 rows.append({'source':relative,'status':'added'if relative in new else'modified','package':pkg,'role':role,'generated':bool(re.search(r'Code generated .*DO NOT EDIT',source)),'sha256':hashlib.sha256(data).hexdigest(),'build_constraint':re.findall(r'^//go:build (.+)',source,re.M),'filename_os_constraint':[os for os in ['darwin','linux','windows']if re.search('_'+os+r'(?:_test)?\.go$',path.name)],'imports':imports(source),'direct_import_consumers':sorted(consumers[package_path]),'declarations':[{k:d.get(k)for k in ['kind','name','names','receiver','signature']}for d in declarations if d['kind']!='import'],'test_names':[d['name']for d in declarations if d.get('name','').startswith('Test')],'doc_anchors':re.findall(r'@project-doc\s+(\S+)',source)})
evidence=root/'refactor/baseline/S12'
with gzip.open(evidence/'final-migration-files.json.gz','wt')as out:json.dump({'files':rows,'symbol_references':'final-symbol-references-{normal,unit,integration}.json.gz','ownership':'migration-ledger.md'},out,ensure_ascii=False)
(evidence/'final-files-for-symbols.json').write_text(json.dumps([r['source']for r in rows if r['status']!='removed'],ensure_ascii=False,indent=2)+'\n')
print('files',len(rows),'added',sum(r['status']=='added'for r in rows),'removed',sum(r['status']=='removed'for r in rows))
