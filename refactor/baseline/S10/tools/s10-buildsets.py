from pathlib import Path
import os,json,subprocess,gzip
root=Path('/Users/daodaoneko/GolandProjects/TokenRouter');out=root/'refactor/baseline/S10';results=[]
for name,tag,goos in [('normal','','darwin'),('unit','unit','darwin'),('integration','integration','darwin'),('wireinject','wireinject','darwin'),('embed','embed','darwin'),('e2e','e2e','darwin'),('linux','','linux')]:
 env=dict(os.environ,GOTOOLCHAIN='go1.27.0',GOOS=goos);cmd=['go','list','-e','-json','-test',*(['-tags='+tag]if tag else []),'./internal/notification/...','./internal/site/...','./internal/moderation/...','./internal/search/...','./internal/identity/...','./internal/billing/...','./internal/service','./internal/repository','./internal/handler/...','./internal/server/...','./internal/web/...','./internal/app/...']
 if goos=='linux':env['CGO_ENABLED']='0'
 p=subprocess.run(cmd,cwd=root/'backend',env=env,text=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
 dec=json.JSONDecoder();raw=p.stdout;pos=0;packages=[]
 while pos<len(raw):
  while pos<len(raw)and raw[pos].isspace():pos+=1
  if pos==len(raw):break
  o,end=dec.raw_decode(raw,pos);pos=end;packages.append({k:o[k]for k in ['ImportPath','Dir','GoFiles','CgoFiles','IgnoredGoFiles','TestGoFiles','XTestGoFiles','Imports','TestImports','XTestImports','EmbedFiles','Error','DepsErrors']if k in o})
 data='buildset-'+name+'.json.gz';(out/data).write_bytes(gzip.compress(json.dumps(packages,ensure_ascii=False).encode()))
 row={'name':name,'command':cmd,'env':{'GOTOOLCHAIN':env['GOTOOLCHAIN'],'GOOS':goos,'CGO_ENABLED':env.get('CGO_ENABLED','default')},'exit_code':p.returncode,'stderr':p.stderr,'packages':len(packages),'errors':[{'package':x['ImportPath'],'Error':x.get('Error'),'DepsErrors':x.get('DepsErrors')} for x in packages if x.get('Error')or x.get('DepsErrors')],'data':data};results.append(row);print(name,len(packages),len(row['errors']),flush=True)
(out/'buildsets.json').write_text(json.dumps(results,ensure_ascii=False,indent=2))
