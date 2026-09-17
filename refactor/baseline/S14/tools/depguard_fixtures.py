# 只创建可丢弃的依赖夹具；finally 恢复全部源文件。
from pathlib import Path
import subprocess,os,json,gzip,time
root=Path(__file__).resolve().parents[4];backend=root/'backend';out=root/'refactor/baseline/S14';prefix='github.com/TokenFlux/TokenRouter/internal/'
results=[]
def run(name,tag,target,expected=None,file=None):
 raw=Path('/tmp/s14-fixture.json')
 if raw.exists():raw.unlink()
 cmd=['golangci-lint','run','--enable-only=depguard','--timeout=5m','--max-same-issues=0','--max-issues-per-linter=0','--output.json.path='+str(raw),*([]if tag=='normal'else['--build-tags='+tag]),target]
 t=time.monotonic();p=subprocess.run(cmd,cwd=backend,env=dict(os.environ,GOTOOLCHAIN='go1.27.0'),stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True)
 issues=json.loads(raw.read_text()).get('Issues',[]) if raw.exists() else []
 match=p.returncode==0 if expected is None else p.returncode==1 and any(x.get('FromLinter')=='depguard' and expected in x.get('Text','') and (not file or x['Pos']['Filename']==file)for x in issues)
 log=f'logs/gate-{name}-{tag}.log.gz';(out/log).write_bytes(gzip.compress(p.stdout.encode()))
 results.append({'name':name,'tag':tag,'command':cmd,'exit_code':p.returncode,'matched':match,'expected_import':expected,'expected_file':file,'issues':issues,'seconds':round(time.monotonic()-t,3),'log':log})
 (out/'depguard-fixtures.json').write_text(json.dumps(results,ensure_ascii=False,indent=2));print(name,tag,match,flush=True)
 if not match:raise RuntimeError(p.stdout+json.dumps(issues,ensure_ascii=False))
def fixture(rel,pkg,imp):
 p=backend/rel;assert not p.exists();p.write_text('// S14 临时依赖夹具。\npackage '+pkg+'\nimport _ "'+imp+'"\n');return p
for tag in ['normal','unit','integration']:
 for name,target in [('core','./internal/backup'),('maintenance','./internal/ops/maintenance'),('provider','./internal/backup/provider'),('http','./internal/backup/httpapi'),('app','./internal/app')]:run('legal-'+name,tag,target)
 for name,rel,pkg,imp in [
 ('new-core-file','internal/backup/zz_s14_gate.go','backup','github.com/robfig/cron/v3'),
 ('core-config','internal/backup/zz_s14_gate.go','backup',prefix+'config'),
 ('core-file-io','internal/backup/zz_s14_gate.go','backup','os'),
 ('new-app-file','internal/app/zz_s14_gate.go','app',prefix+'service'),
 ('http-database','internal/backup/httpapi/zz_s14_gate.go','httpapi','database/sql'),
 ('core-reverse','internal/ops/maintenance/zz_s14_gate.go','maintenance',prefix+'ops/provider'),
 ]:
  p=fixture(rel,pkg,imp)
  try:run(name,tag,'./'+str(Path(rel).parent),imp,rel)
  finally:p.unlink()
 p=backend/'internal/backup/runtime.go';original=p.read_bytes();p.write_text(original.decode().replace('import (','import (\n _ "github.com/gin-gonic/gin"',1))
 try:run('old-file-new-dependency',tag,'./internal/backup','github.com/gin-gonic/gin','internal/backup/runtime.go')
 finally:p.write_bytes(original)
 moved=backend/'internal/backup/zz_s14_moved.go';assert not moved.exists();p.rename(moved)
 try:run('moved-permission-expired',tag,'./internal/backup','github.com/robfig/cron/v3','internal/backup/zz_s14_moved.go')
 finally:moved.rename(p)
 p=fixture('internal/backup/httpapi/zz_s14_gate.go','httpapi',prefix+'server/httpx')
 try:run('normal-adapter',tag,'./internal/backup/httpapi')
 finally:p.unlink()
print('ALL_MATCHED',len(results),flush=True)
