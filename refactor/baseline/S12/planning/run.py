import subprocess,os,sys,json,time,pathlib
root=pathlib.Path('/Users/daodaoneko/GolandProjects/TokenRouter');out=pathlib.Path('/tmp/tokenrouter-s12-planning');label=sys.argv[1];cmd=sys.argv[2:];start=time.monotonic()
with(out/(label+'.log')).open('w')as f:r=subprocess.run(cmd,cwd=root/'backend',env=dict(os.environ,GOTOOLCHAIN='go1.27.0'),stdout=f,stderr=subprocess.STDOUT)
events=[]
for line in(out/(label+'.log')).read_text().splitlines():
 try:e=json.loads(line)
 except ValueError:continue
 if isinstance(e,dict):events.append(e)
summary={'command':cmd,'exit_code':r.returncode,'seconds':round(time.monotonic()-start,3),**{a:sum(e.get('Action')==a and bool(e.get('Test'))for e in events)for a in ['pass','fail','skip']}}
(out/(label+'.result.json')).write_text(json.dumps(summary,ensure_ascii=False,indent=2));print(json.dumps(summary,ensure_ascii=False),flush=True)
if r.returncode:
 failed={e.get('Test')for e in events if e.get('Action')=='fail'}
 print(''.join(e.get('Output','')for e in events if e.get('Test')in failed)[-5500:])
