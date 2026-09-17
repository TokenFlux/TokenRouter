from pathlib import Path
import subprocess,os,json,gzip,time,shutil
root=Path('/Users/daodaoneko/GolandProjects/TokenRouter');backend=root/'backend';out=root/'refactor/baseline/S13';env=dict(os.environ,GOTOOLCHAIN='go1.27.0');results=[]
prefix='github.com/TokenFlux/TokenRouter/internal/'
def run(name,tag,target,expected=None,file=None):
 raw=Path('/tmp')/f's13-fixture-{name}-{tag}.json'
 if raw.exists():raw.unlink()
 cmd=['golangci-lint','run','--enable-only=depguard','--timeout=5m','--max-same-issues=0','--max-issues-per-linter=0','--output.json.path='+str(raw),*([]if tag=='normal'else['--build-tags='+tag]),target]
 start=time.monotonic();p=subprocess.run(cmd,cwd=backend,env=env,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True)
 issues=(json.loads(raw.read_text()).get('Issues') or []) if raw.exists() else []
 hits=[i for i in issues if i.get('FromLinter')=='depguard' and expected and expected in i.get('Text','') and (not file or i['Pos']['Filename']==file)]
 matched=(p.returncode==0) if expected is None else (p.returncode==1 and bool(hits))
 log='logs/gate-'+name+'-'+tag+'.log.gz';(out/log).write_bytes(gzip.compress(p.stdout.encode()))
 row={'name':name,'tag':tag,'command':cmd,'exit_code':p.returncode,'expected_import':expected,'expected_file':file,'matched':matched,'issues':issues,'seconds':round(time.monotonic()-start,3),'log':log};results.append(row)
 (out/'depguard-fixtures.json').write_text(json.dumps(results,ensure_ascii=False,indent=2));print(json.dumps({k:v for k,v in row.items() if k!='issues'},ensure_ascii=False),flush=True)
 if not matched:raise RuntimeError(p.stdout)
def fixture(rel,body):
 p=backend/rel;assert not p.exists(),p;p.write_text('// S13 可丢弃门禁夹具，验证后删除。\n'+body);return p
def inject(rel,imp):
 p=backend/rel;old=p.read_bytes();s=old.decode();assert 'import (' in s;p.write_text(s.replace('import (','import (\n _ "'+imp+'"',1));return p,old
for tag in ['normal','unit','integration']:
 for name,target in [('creative-core','./internal/creative'),('batch-core','./internal/batchimage'),('http','./internal/batchimage/httpapi'),('postgres','./internal/batchimage/postgres'),('app','./internal/app')]:
  run('legal-'+name,tag,target)
 for name,rel,pkg,imp in [
  ('new-file-no-permission','internal/creative/zz_s13_gate.go','creative',prefix+'billing'),
  ('core-http-denied','internal/creative/zz_s13_gate.go','creative','net/http'),
  ('core-config-denied','internal/batchimage/zz_s13_gate.go','batchimage',prefix+'config'),
  ('new-app-file-denied','internal/app/zz_s13_gate.go','app',prefix+'service'),
  ('upstream-platform-direction','internal/upstream/qoder/zz_s13_gate.go','qoder',prefix+'upstream/grok'),
  ('http-storage-denied','internal/batchimage/httpapi/zz_s13_gate.go','httpapi','database/sql'),
  ('protocol-stdlib-preserved','internal/protocol/openai/zz_s13_gate.go','openai','crypto/sha256'),
 ]:
  p=fixture(rel,'package '+pkg+'\nimport _ "'+imp+'"\n')
  try:run(name,tag,'./'+str(Path(rel).parent),imp,rel)
  finally:p.unlink()
 p,old=inject('internal/creative/public.go','github.com/gin-gonic/gin')
 try:run('existing-file-new-import-denied',tag,'./internal/creative','github.com/gin-gonic/gin','internal/creative/public.go')
 finally:p.write_bytes(old)
 p=backend/'internal/creative/public.go';moved=backend/'internal/creative/zz_s13_moved.go';assert not moved.exists();p.rename(moved)
 try:run('moved-file-exception-expires',tag,'./internal/creative',prefix+'billing','internal/creative/zz_s13_moved.go')
 finally:moved.rename(p)
 directory=backend/'internal/creative/zz_s13_leaf';assert not directory.exists();directory.mkdir()
 p=fixture('internal/creative/zz_s13_leaf/child.go','package child\nimport _ "'+prefix+'app"\n')
 try:run('core-to-app-denied',tag,'./internal/creative/zz_s13_leaf',prefix+'app','internal/creative/zz_s13_leaf/child.go')
 finally:p.unlink();directory.rmdir()
 directory=backend/'internal/billing/zz_s13_child';assert not directory.exists();directory.mkdir()
 child=fixture('internal/billing/zz_s13_child/child.go','package child\n')
 p,old=inject('internal/creative/public.go',prefix+'billing/zz_s13_child')
 try:run('exact-import-rejects-subpackage',tag,'./internal/creative',prefix+'billing/zz_s13_child','internal/creative/public.go')
 finally:p.write_bytes(old);child.unlink();directory.rmdir()
 rel='internal/batchimage/httpapi/zz_s13_gate.go';p=fixture(rel,'package httpapi\nimport(_ "'+prefix+'batchimage";_ "'+prefix+'server/httpx")\n')
 try:run('new-legal-http-adapter',tag,'./internal/batchimage/httpapi')
 finally:p.unlink()
print('ALL_FIXTURES_MATCHED',len(results),flush=True)
