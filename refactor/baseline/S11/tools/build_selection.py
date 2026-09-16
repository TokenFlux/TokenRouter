# 构建集合只提供文件选择证据；实际行为另看 JSON 测试事件。
from pathlib import Path
import os,subprocess,json,gzip,time
root=Path(__file__).resolve().parents[4];out=root/'refactor/baseline/S11';results=[]
variants=[('normal',[],{}),('unit',['-tags=unit'],{}),('integration',['-tags=integration'],{}),('wireinject',['-tags=wireinject'],{}),('embed',['-tags=embed'],{}),('e2e',['-tags=e2e'],{}),('darwin',[],{'GOOS':'darwin','GOARCH':'arm64'}),('linux',[],{'GOOS':'linux','GOARCH':'amd64','CGO_ENABLED':'0'})]
for name,flags,extra in variants:
 cmd=['go','list','-json','-test',*flags,'./...'];start=time.monotonic()
 proc=subprocess.run(cmd,cwd=root/'backend',env=dict(os.environ,GOTOOLCHAIN='go1.27.0',**extra),stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
 values=[];raw=proc.stdout;decoder=json.JSONDecoder();pos=0
 while pos<len(raw):
  while pos<len(raw)and raw[pos].isspace():pos+=1
  if pos==len(raw):break
  value,end=decoder.raw_decode(raw,pos);pos=end
  values.append({k:value[k]for k in ['ImportPath','Name','ForTest','GoFiles','CgoFiles','TestGoFiles','XTestGoFiles','IgnoredGoFiles','EmbedFiles','TestEmbedFiles','XTestEmbedFiles','Error']if k in value})
 with gzip.open(out/('build-selection-'+name+'.json.gz'),'wt')as stream:json.dump(values,stream,ensure_ascii=False)
 row={'variant':name,'command':cmd,'environment':{'GOTOOLCHAIN':'go1.27.0',**extra},'exit_code':proc.returncode,'packages':len(values),'seconds':round(time.monotonic()-start,3),'stderr':proc.stderr,'data':'build-selection-'+name+'.json.gz'}
 results.append(row);(out/'build-selection-results.json').write_text(json.dumps(results,ensure_ascii=False,indent=2)+'\n');print(name,proc.returncode,len(values),flush=True)
 if proc.returncode:raise SystemExit(proc.returncode)
