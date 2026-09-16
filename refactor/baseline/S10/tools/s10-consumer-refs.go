// 临时只读清点工具：使用 Go 类型信息定位真实静态引用，不参与依赖门禁。
package main
import("compress/gzip";"encoding/json";"fmt";"go/token";"go/types";"os";"path/filepath";"sort";"strings";"golang.org/x/tools/go/packages")
type Ref struct{From string `json:"from"`;Line int `json:"line"`;Column int `json:"column"`;Target string `json:"target"`;TargetLine int `json:"target_line"`;Package string `json:"package"`;Symbol string `json:"symbol"`;Kind string `json:"kind"`}
func main(){
 root,_:=filepath.Abs("..");tag:=os.Args[1];out:=os.Args[2];flags:=[]string{};if tag!="normal"{flags=append(flags,"-tags="+tag)}
 cfg:=&packages.Config{Mode:packages.NeedName|packages.NeedFiles|packages.NeedCompiledGoFiles|packages.NeedTypes|packages.NeedTypesInfo|packages.NeedSyntax|packages.NeedImports,Tests:true,BuildFlags:flags,Fset:token.NewFileSet()}
 pkgs,err:=packages.Load(cfg,"./internal/...", "./cmd/...");if err!=nil{panic(err)};if packages.PrintErrors(pkgs)>0{os.Exit(1)}
 seen:=map[string]bool{};refs:=[]Ref{}
 local:=func(path string)string{p,err:=filepath.Rel(root,path);if err!=nil{return ""};p=filepath.ToSlash(p);if strings.HasPrefix(p,"backend/internal/")||strings.HasPrefix(p,"backend/cmd/"){return p};return ""}
 for _,pkg:=range pkgs{if pkg.TypesInfo==nil{continue};for id,obj:=range pkg.TypesInfo.Uses{
  if obj.Pkg()==nil||!strings.HasPrefix(obj.Pkg().Path(),"github.com/TokenFlux/TokenRouter/internal/"){continue};symbol:=obj.Name();kind:=""
  switch item:=obj.(type){case *types.Func:kind="func";sig,ok:=item.Type().(*types.Signature);if ok&&sig.Recv()!=nil{recv:=sig.Recv().Type();if p,ok:=recv.(*types.Pointer);ok{recv=p.Elem()};if n,ok:=recv.(*types.Named);ok{symbol=n.Obj().Name()+"."+symbol}}
  case *types.TypeName:kind="type";case *types.Const:kind="const";default:continue}
  at:=cfg.Fset.Position(id.Pos());target:=cfg.Fset.Position(obj.Pos());from,to:=local(at.Filename),local(target.Filename);if from==""||to==""{continue}
  key:=fmt.Sprintf("%s:%d:%d:%s:%d:%s",from,at.Line,at.Column,to,target.Line,symbol);if seen[key]{continue};seen[key]=true
  refs=append(refs,Ref{from,at.Line,at.Column,to,target.Line,obj.Pkg().Path(),symbol,kind})
 }}
 sort.Slice(refs,func(i,j int)bool{a,b:=refs[i],refs[j];if a.From!=b.From{return a.From<b.From};if a.Line!=b.Line{return a.Line<b.Line};if a.Column!=b.Column{return a.Column<b.Column};return a.Target<b.Target})

 // 结构实现候选不代表实际装配；Wire 引用和构造器调用另在 references 中保留。
 type contract struct{pkg,name string;iface *types.Interface}
 type candidate struct{pkg,name string;named *types.Named}
 contracts:=[]contract{};candidates:=[]candidate{};typeSeen:=map[string]bool{}
 for _,pkg:=range pkgs{if pkg.Types==nil||strings.Contains(pkg.PkgPath," ["){continue};if !strings.HasPrefix(pkg.PkgPath,"github.com/TokenFlux/TokenRouter/internal/"){continue};scope:=pkg.Types.Scope();for _,name:=range scope.Names(){obj,ok:=scope.Lookup(name).(*types.TypeName);if !ok{continue};key:=pkg.PkgPath+"."+name;if typeSeen[key]{continue};typeSeen[key]=true
  typ:=types.Unalias(obj.Type());named,ok:=typ.(*types.Named);if !ok{continue};if named.TypeParams().Len()>0{continue}
  if iface,ok:=named.Underlying().(*types.Interface);ok{if iface.NumMethods()==0||!iface.IsMethodSet(){continue};short:=strings.TrimPrefix(pkg.PkgPath,"github.com/TokenFlux/TokenRouter/internal/");if short=="notification"||short=="site"||short=="moderation"||short=="search"||short=="identity"||short=="billing"||strings.HasPrefix(short,"notification/")||strings.HasPrefix(short,"moderation/")||strings.HasPrefix(short,"search/")||strings.HasPrefix(short,"site/"){contracts=append(contracts,contract{pkg.PkgPath,name,iface})};continue};candidates=append(candidates,candidate{pkg.PkgPath,name,named})
 }}
 implementations:=[]map[string]string{}
 for _,c:=range contracts{for _,v:=range candidates{value:=types.Implements(v.named,c.iface);pointer:=types.Implements(types.NewPointer(v.named),c.iface);if value||pointer{kind:="pointer";if value{kind="value_and_pointer"};implementations=append(implementations,map[string]string{"contract":c.pkg+"."+c.name,"candidate":v.pkg+"."+v.name,"receiver":kind})}}}
 f,err:=os.Create(out);if err!=nil{panic(err)};z:=gzip.NewWriter(f);err=json.NewEncoder(z).Encode(map[string]any{"build_tag":tag,"note":"静态类型引用包含接口声明，不把接口引用当成动态实现；生产实现绑定另由 Wire/装配清单确认。","references":refs,"structural_implementations":implementations});if err!=nil{panic(err)};if err=z.Close();err!=nil{panic(err)};if err=f.Close();err!=nil{panic(err)};fmt.Printf("%s: %d references across %d loaded packages\n",tag,len(refs),len(pkgs))
}
