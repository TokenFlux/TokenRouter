from pathlib import Path
import subprocess,os,json,gzip,time,shutil
root=Path('/Users/daodaoneko/GolandProjects/TokenRouter');backend=root/'backend';out=root/'refactor/baseline/S10';env=dict(os.environ,GOTOOLCHAIN='go1.27.0');results=[]
prefix='github.com/TokenFlux/TokenRouter/internal/'
def run(name,tag,target,expected=None,file=None):
 raw=Path('/tmp')/f's10-fixture-{name}-{tag}.json'
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
 p=backend/rel;assert not p.exists(),p;p.write_text('// S10 可丢弃门禁夹具，验证后删除。\n'+body);return p
def inject(rel,imp):
 p=backend/rel;old=p.read_bytes();s=old.decode();assert 'import (' in s;p.write_text(s.replace('import (','import (\n _ "'+imp+'"',1));return p,old
for tag in ['normal','unit','integration']:
 for module in ['notification','site','moderation','search']:
  run('legal-'+module,tag,'./internal/'+module+'/...',None)
 run('legal-app-adaptation',tag,'./internal/app',None)
 run('legal-legacy-search',tag,'./internal/pkg/websearch/...',None)
 for name,rel,pkg,imp in [
  ('legacy-search-new-file-denied','internal/pkg/websearch/zz_s10_gate.go','websearch',prefix+'search/provider'),
  ('new-core-file-no-exception','internal/moderation/zz_s10_gate.go','moderation',prefix+'settings'),
  ('core-to-http-adapter-denied','internal/moderation/zz_s10_gate.go','moderation',prefix+'server/httpx'),
  ('search-core-to-provider-denied','internal/search/zz_s10_gate.go','search',prefix+'search/provider'),
  ('new-app-file-denied','internal/app/zz_s10_gate.go','app',prefix+'service'),
  ('deleted-bridge-permission-expired','internal/app/legacybridge/team.go','legacybridge',prefix+'service'),
  ('http-storage-denied','internal/notification/httpapi/zz_s10_gate.go','httpapi','database/sql'),
  ('protocol-stdlib-preserved','internal/protocol/openai/zz_s10_gate.go','openai','crypto/sha256'),
 ]:
  p=fixture(rel,'package '+pkg+'\nimport _ "'+imp+'"\n')
  try:run(name,tag,'./'+str(Path(rel).parent),imp,rel)
  finally:p.unlink()
 p,old=inject('internal/moderation/ports.go','github.com/gin-gonic/gin')
 try:run('old-file-new-coupling-denied',tag,'./internal/moderation','github.com/gin-gonic/gin','internal/moderation/ports.go')
 finally:p.write_bytes(old)
 p=backend/'internal/moderation/ports.go';moved=backend/'internal/moderation/zz_s10_moved.go';assert not moved.exists();p.rename(moved)
 try:run('moved-file-permission-expired',tag,'./internal/moderation',prefix+'settings','internal/moderation/zz_s10_moved.go')
 finally:moved.rename(p)
 directory=backend/'internal/notification/contract/zz_s10_child';assert not directory.exists();directory.mkdir()
 child=fixture('internal/notification/contract/zz_s10_child/child.go','package child\n')
 p,old=inject('internal/notification/ports.go',prefix+'notification/contract/zz_s10_child')
 try:run('exact-import-denies-subpackage',tag,'./internal/notification',prefix+'notification/contract/zz_s10_child','internal/notification/ports.go')
 finally:p.write_bytes(old);child.unlink();directory.rmdir()
 directory.mkdir();child=fixture('internal/notification/contract/zz_s10_child/child.go','package child\nimport _ "'+prefix+'settings"\n')
 try:run('leaf-subpackage-role-applies',tag,'./internal/notification/contract/...',prefix+'settings','internal/notification/contract/zz_s10_child/child.go')
 finally:child.unlink();directory.rmdir()
 rel='internal/notification/httpapi/zz_s10_gate.go';p=fixture(rel,'package httpapi\nimport(_ "'+prefix+'notification";_ "'+prefix+'server/httpx")\n')
 try:run('new-legal-http-adapter',tag,'./internal/notification/httpapi',None)
 finally:p.unlink()
print('ALL_FIXTURES_MATCHED',len(results),flush=True)
