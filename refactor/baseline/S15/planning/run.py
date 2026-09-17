import subprocess,pathlib,json,os,sys,collections,time
p=pathlib.Path('/tmp/tokenrouter-s15-planning');name=sys.argv[1];cmd=sys.argv[2:];env=dict(os.environ,GOTOOLCHAIN='go1.27.0');start=time.time()
with (p/(name+'.log')).open('w') as f: r=subprocess.run(cmd,cwd='/Users/daodaoneko/GolandProjects/TokenRouter/backend',env=env,stdout=f,stderr=subprocess.STDOUT)
c=collections.Counter(); failures=[]
for l in (p/(name+'.log')).read_text().splitlines():
 try:e=json.loads(l)
 except:continue
 if e.get('Test') and e.get('Action') in ('pass','fail','skip'):c[e['Action']]+=1
 if e.get('Action')=='fail':failures.append([e.get('Package'),e.get('Test')])
d={'command':cmd,'exit':r.returncode,'events':dict(c),'failures':failures,'seconds':time.time()-start};(p/(name+'.json')).write_text(json.dumps(d,indent=2));print(json.dumps(d));sys.exit(r.returncode)
