# 只校验本阶段涉及的本地文档链接与代码稳定锚点。
from pathlib import Path
import re,json,subprocess,urllib.parse
root=Path(__file__).resolve().parents[4];out=root/'refactor/baseline/S11';missing=[];checked=[]
changed=subprocess.check_output(['git','diff','--name-only'],cwd=root,text=True).splitlines()
new=subprocess.check_output(['git','ls-files','--others','--exclude-standard'],cwd=root,text=True).splitlines()
def anchors(p):
 s=p.read_text();found=set(re.findall(r'<a\s+(?:id|name)="([^"]+)"',s));counts={}
 for title in re.findall(r'^#{1,6}\s+(.+?)\s*#*$',s,re.M):
  slug=re.sub(r'[^\w\-\s\u4e00-\u9fff]','',title.lower()).replace(' ','-');n=counts.get(slug,0);counts[slug]=n+1;found.add(slug+('-'+str(n)if n else''))
 return found
files=[root/p for p in changed+new if p.endswith('.md') and (p.startswith('docs/')or p=='refactor/S11-gateway-orchestration.md'or p.startswith('refactor/baseline/S11/'))]
for p in files:
 if not p.is_file():continue
 source=p.read_text()
 for target in re.findall(r'(?<!!)\[[^\]]*\]\(([^)]+)\)',source):
  target=target.strip('<>');target=target.split(' "',1)[0]
  if re.match(r'^[a-zA-Z][a-zA-Z0-9+.-]*:',target):continue
  name,sep,anchor=target.partition('#');name=urllib.parse.unquote(name)
  q=(p.parent/name).resolve()if name else p
  ok=q.exists()
  if ok and sep and q.suffix=='.md':ok=urllib.parse.unquote(anchor)in anchors(q)
  row={'source':str(p.relative_to(root)),'target':target,'ok':ok};checked.append(row)
  if not ok:missing.append(row)
for path in changed+new:
 if not path.endswith('.go'):continue
 p=root/path
 if not p.is_file():continue
 for target in re.findall(r'@project-doc\s+(\S+)',p.read_text()):
  name,_,anchor=target.partition('#');q=root/name;ok=q.exists()and(not anchor or anchor in anchors(q));row={'source':path,'target':target,'ok':ok};checked.append(row)
  if not ok:missing.append(row)
(out/'final-doc-links.json').write_text(json.dumps({'checked':len(checked),'missing':missing,'links':checked},ensure_ascii=False,indent=2)+'\n');print('checked',len(checked),'missing',len(missing))
for row in missing:print(row)
