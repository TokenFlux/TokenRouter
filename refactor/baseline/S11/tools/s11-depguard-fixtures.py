from pathlib import Path
import subprocess,os,json,gzip,time,shutil
root=Path('/Users/daodaoneko/GolandProjects/TokenRouter');backend=root/'backend';out=root/'refactor/baseline/S11';env=dict(os.environ,GOTOOLCHAIN='go1.27.0');results=[]
prefix='github.com/TokenFlux/TokenRouter/internal/'
def run(name,tag,target,expected=None,file=None):
 raw=Path('/tmp')/f's11-fixture-{name}-{tag}.json'
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
 p=backend/rel;assert not p.exists(),p;p.write_text('// S11 可丢弃门禁夹具，验证后删除。\n'+body);return p
def inject(rel,imp):
 p=backend/rel;old=p.read_bytes();s=old.decode();assert 'import (' in s;p.write_text(s.replace('import (','import (\n _ "'+imp+'"',1));return p,old
for tag in ['normal','unit','integration']:
 for name,target in [('text-core','./internal/gateway/text'),('error-core','./internal/gateway/errorpolicy'),('http-adapter','./internal/gateway/httpapi'),('storage-adapter','./internal/gateway/postgres'),('redis-adapter','./internal/gateway/rediscache'),('app-adaptation','./internal/app')]:
  run('legal-'+name,tag,target,None)
 for name,rel,pkg,imp in [
  ('new-file-no-historical-permission','internal/gateway/errorpolicy/zz_s11_gate.go','errorpolicy',prefix+'egress'),
  ('core-config-denied','internal/gateway/errorpolicy/zz_s11_gate.go','errorpolicy',prefix+'config'),
  ('core-http-denied','internal/gateway/errorpolicy/zz_s11_gate.go','errorpolicy','net/http'),
  ('new-app-file-denied','internal/app/zz_s11_gate.go','app',prefix+'service'),
  ('deleted-bridge-permission-expired','internal/app/legacybridge/qoder_chat.go','legacybridge',prefix+'service'),
  ('http-storage-denied','internal/gateway/httpapi/zz_s11_gate.go','httpapi','database/sql'),
  ('protocol-stdlib-preserved','internal/protocol/openai/zz_s11_gate.go','openai','crypto/sha256'),
 ]:
  p=fixture(rel,'package '+pkg+'\nimport _ "'+imp+'"\n')
  try:run(name,tag,'./'+str(Path(rel).parent),imp,rel)
  finally:p.unlink()
 p,old=inject('internal/gateway/errorpolicy/rule.go','github.com/gin-gonic/gin')
 try:run('approved-file-new-coupling-denied',tag,'./internal/gateway/errorpolicy','github.com/gin-gonic/gin','internal/gateway/errorpolicy/rule.go')
 finally:p.write_bytes(old)
 p=backend/'internal/gateway/errorpolicy/rule.go';moved=backend/'internal/gateway/errorpolicy/zz_s11_moved.go';assert not moved.exists();p.rename(moved)
 try:run('moved-file-permission-expired',tag,'./internal/gateway/errorpolicy',prefix+'egress','internal/gateway/errorpolicy/zz_s11_moved.go')
 finally:moved.rename(p)
 # 新 core 子包未被 app 消费，因此反向依赖夹具不会先触发 Go import cycle。
 directory=backend/'internal/gateway/zz_s11_leaf';assert not directory.exists();directory.mkdir()
 p=fixture('internal/gateway/zz_s11_leaf/child.go','package child\nimport _ "'+prefix+'app"\n')
 try:run('core-to-app-denied',tag,'./internal/gateway/zz_s11_leaf',prefix+'app','internal/gateway/zz_s11_leaf/child.go')
 finally:p.unlink();directory.rmdir()
 # 纯执行契约叶子的子包也不能反向 import 根执行器。
 directory=backend/'internal/gateway/execution/zz_s11_leaf';assert not directory.exists();directory.mkdir()
 p=fixture('internal/gateway/execution/zz_s11_leaf/child.go','package child\nimport _ "'+prefix+'gateway"\n')
 try:run('execution-leaf-reverse-denied',tag,'./internal/gateway/execution/zz_s11_leaf',prefix+'gateway','internal/gateway/execution/zz_s11_leaf/child.go')
 finally:p.unlink();directory.rmdir()
 directory=backend/'internal/egress/zz_s11_child';assert not directory.exists();directory.mkdir()
 child=fixture('internal/egress/zz_s11_child/child.go','package child\n')
 p,old=inject('internal/gateway/errorpolicy/rule.go',prefix+'egress/zz_s11_child')
 try:run('exact-import-denies-subpackage',tag,'./internal/gateway/errorpolicy',prefix+'egress/zz_s11_child','internal/gateway/errorpolicy/rule.go')
 finally:p.write_bytes(old);child.unlink();directory.rmdir()
 rel='internal/gateway/httpapi/zz_s11_gate.go';p=fixture(rel,'package httpapi\nimport(_ "'+prefix+'gateway";_ "'+prefix+'server/httpx")\n')
 try:run('new-legal-http-adapter',tag,'./internal/gateway/httpapi',None)
 finally:p.unlink()
print('ALL_FIXTURES_MATCHED',len(results),flush=True)
