from pathlib import Path
import json,gzip,collections,re,hashlib,shutil
r=Path('/Users/daodaoneko/GolandProjects/TokenRouter');o=r/'refactor/baseline/S10'
files=json.loads(gzip.decompress((o/'final-file-declarations.json.gz').read_bytes()))
refs={};implementations={}
for tag in ['normal','unit','integration']:
 data=json.loads(gzip.decompress((o/f'final-consumer-refs-{tag}.json.gz').read_bytes()));refs[tag]=data['references'];implementations[tag]=data['structural_implementations']
 by_from=collections.defaultdict(list);by_target=collections.defaultdict(list)
 for x in refs[tag]:by_from[x['from']].append(x);by_target[x['target']].append(x)
 refs[tag]=(by_from,by_target)
rows=[]
def role(path):
 parts=path.split('/');module=parts[2]
 if module in ['notification','site','moderation','search','identity','billing','team','ops']:
  sub=parts[3] if len(parts)>4 else''
  return {'owner':module,'role':sub if sub in ['contract','httpapi','postgres','rediscache','provider','smtp','filesystem']else'core'}
 if module=='app':return {'owner':'app','role':'legacybridge'if '/legacybridge/'in path else'composition/lifecycle'}
 if module=='server':return {'owner':'server','role':'HTTP route binding'}
 if module in ['service','repository','handler','pkg']:return {'owner':'legacy compatibility / retained caller','role':'mixed; inspect actual symbol links','exit':'S11–S16 (handoff.md)'}
 return {'owner':module,'role':'consumer'}
for file in files:
 if file['status']=='deleted':continue
 path=file['path'];s=(r/path).read_text()
 for d in file['declarations']:
  if d['kind']=='import':continue
  start=d['line'];end=s.count('\n',0,d['end'])+1
  name=d.get('name')or','.join(d.get('names',[]))
  incoming={};outgoing={}
  for tag,(by_from,by_target) in refs.items():
   incoming[tag]=[x for x in by_target.get(path,[])if start<=x['target_line']<=end]
   outgoing[tag]=[x for x in by_from.get(path,[])if start<=x['line']<=end]
  rows.append({'path':path,'symbol':name,'receiver':d.get('receiver'),'kind':d['kind'],'line':start,'end_line':end,'ownership':role(path),'build_constraints':file['build_constraints'],'test':path.endswith('_test.go'),'generated':file['generated'],'direct_consumers':incoming,'referenced_symbols':outgoing})
(o/'final-symbol-links.json.gz').write_bytes(gzip.compress(json.dumps(rows,ensure_ascii=False).encode()))
# 实际生产构造、路由、生命周期和结构实现索引；结构符合不等同于生产注入。
wire=[x for x in refs['normal'][0].get('backend/internal/app/wire_gen.go',[]) if any('/'+m+'/'in x['target']for m in ['notification','site','moderation','search','identity','billing','team'])]
(o/'final-wire-module-bindings.json').write_text(json.dumps(wire,ensure_ascii=False,indent=2))
summary={'files':len(files),'declarations_excluding_imports':len(rows),'static_references':{t:sum(map(len,v[0].values()))for t,v in refs.items()},'structural_implementations':{t:len(v)for t,v in implementations.items()},'production_wire_references':len(wire),'note':'逐符号消费者来自 Go 类型信息，包含具体构造和 Wire 引用；不把接口静态引用当成动态执行证明。混合旧文件含未迁声明，保留归属见 migration-ledger 与 handoff。'}
(o/'final-migration-summary.json').write_text(json.dumps(summary,ensure_ascii=False,indent=2));print(summary)
# 构建和辅助验收按实际结果登记。
labels=['final-backend-build','final-frontend-test','final-frontend-build','final-embed-test','final-embed-build','final-linux-build','final-jwtgen-build','final-cleanup-command-build','final-wire-idempotence','final-process-storage-contracts']
(o/'final-build-results.json').write_text(json.dumps([{'label':n,**json.loads((o/(n+'.result.json')).read_text())}for n in labels],ensure_ascii=False,indent=2))
# 原失败诊断与本阶段失败记录均可审查；中间 lint 的完整诊断单独保留。
failures=[]
for p in sorted(o.glob('*.result.json')):
 d=json.loads(p.read_text())
 if d.get('exit_code')!=0:failures.append({'result':p.name,'exit_code':d.get('exit_code'),'command':d.get('command'),'classification':'expected-existing-lint'if p.name.startswith('final-')and '-lint.'in p.name else'migration/intermediate; superseded by final validation'})
(o/'intermediate-results.json').write_text(json.dumps(failures,ensure_ascii=False,indent=2))
(o/'tools').mkdir(exist_ok=True)
for name in ['s10-depguard-fixtures.py','s10-buildsets.py','s10-consumer-refs.go','s10-final-inventory.py','s10-evidence-index.py']:
 shutil.copyfile('/tmp/'+name,o/'tools'/name)
