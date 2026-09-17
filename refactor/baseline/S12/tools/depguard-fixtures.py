from pathlib import Path
import subprocess,os,json,gzip,time,shutil
root=Path('/Users/daodaoneko/GolandProjects/TokenRouter');backend=root/'backend';out=root/'refactor/baseline/S12';env=dict(os.environ,GOTOOLCHAIN='go1.27.0');results=[]
prefix='github.com/TokenFlux/TokenRouter/internal/'
def run(name,tag,target,expected=None,file=None):
 raw=Path('/tmp')/f's12-fixture-{name}-{tag}.json'
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
 p=backend/rel;assert not p.exists(),p;p.write_text('// S12 可丢弃门禁夹具，验证后删除。\n'+body);return p
def inject(rel,imp):
 p=backend/rel;old=p.read_bytes();s=old.decode();assert 'import (' in s;p.write_text(s.replace('import (','import (\n _ "'+imp+'"',1));return p,old
for tag in ['normal','unit','integration']:
 for name,target in [('promotion-core','./internal/promotion'),('payment-core','./internal/payment'),('http','./internal/payment/httpapi'),('postgres','./internal/payment/postgres'),('app','./internal/app')]:
  run('legal-'+name,tag,target)
 for name,rel,pkg,imp in [
  ('new-file-no-permission','internal/payment/zz_s12_gate.go','payment',prefix+'billing'),
  ('core-http-denied','internal/payment/zz_s12_gate.go','payment','net/http'),
  ('core-config-denied','internal/promotion/zz_s12_gate.go','promotion',prefix+'config'),
  ('new-app-file-denied','internal/app/zz_s12_gate.go','app',prefix+'service'),
  ('retired-payment-wire-permission','internal/payment/wire.go','payment',prefix+'config'),
  ('http-storage-denied','internal/payment/httpapi/zz_s12_gate.go','httpapi','database/sql'),
  ('protocol-stdlib-preserved','internal/protocol/openai/zz_s12_gate.go','openai','crypto/sha256'),
 ]:
  p=fixture(rel,'package '+pkg+'\nimport _ "'+imp+'"\n')
  try:run(name,tag,'./'+str(Path(rel).parent),imp,rel)
  finally:p.unlink()
 p,old=inject('internal/payment/order.go','github.com/gin-gonic/gin')
 try:run('existing-file-new-import-denied',tag,'./internal/payment','github.com/gin-gonic/gin','internal/payment/order.go')
 finally:p.write_bytes(old)
 p=backend/'internal/payment/order.go';moved=backend/'internal/payment/zz_s12_moved.go';assert not moved.exists();p.rename(moved)
 try:run('moved-file-exception-expires',tag,'./internal/payment',prefix+'billing','internal/payment/zz_s12_moved.go')
 finally:moved.rename(p)
 directory=backend/'internal/payment/zz_s12_leaf';assert not directory.exists();directory.mkdir()
 p=fixture('internal/payment/zz_s12_leaf/child.go','package child\nimport _ "'+prefix+'app"\n')
 try:run('core-to-app-denied',tag,'./internal/payment/zz_s12_leaf',prefix+'app','internal/payment/zz_s12_leaf/child.go')
 finally:p.unlink();directory.rmdir()
 directory=backend/'internal/billing/zz_s12_child';assert not directory.exists();directory.mkdir()
 child=fixture('internal/billing/zz_s12_child/child.go','package child\n')
 p,old=inject('internal/payment/order.go',prefix+'billing/zz_s12_child')
 try:run('exact-import-rejects-subpackage',tag,'./internal/payment',prefix+'billing/zz_s12_child','internal/payment/order.go')
 finally:p.write_bytes(old);child.unlink();directory.rmdir()
 rel='internal/payment/httpapi/zz_s12_gate.go';p=fixture(rel,'package httpapi\nimport(_ "'+prefix+'payment";_ "'+prefix+'server/httpx")\n')
 try:run('new-legal-http-adapter',tag,'./internal/payment/httpapi')
 finally:p.unlink()
print('ALL_FIXTURES_MATCHED',len(results),flush=True)
