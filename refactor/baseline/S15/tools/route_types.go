package main
import("encoding/json";"go/ast";"go/types";"os";"golang.org/x/tools/go/packages";"go/parser";"go/token";"path/filepath")
func render(t types.Type) string {
 switch x:=t.(type){
 case *types.Alias:return render(types.Unalias(x))
 case *types.Pointer:return "*"+render(x.Elem())
 case *types.Named:
 s:="@"+x.Obj().Pkg().Path()+"@."+x.Obj().Name();if x.TypeArgs().Len()>0{s+="[";for i:=0;i<x.TypeArgs().Len();i++{if i>0{s+=","};s+=render(x.TypeArgs().At(i))};s+="]"};return s
 default:return types.TypeString(t,func(p *types.Package)string{return "@"+p.Path()+"@"})
 }
}
func main(){
 ps,err:=packages.Load(&packages.Config{Mode:packages.NeedName|packages.NeedTypes|packages.NeedImports|packages.NeedDeps},"./internal/handler");if err!=nil{panic(err)};if packages.PrintErrors(ps)>0{os.Exit(1)}
 fields:=map[string]string{};for _,name:=range []string{"Handlers","AdminHandlers"}{t:=ps[0].Types.Scope().Lookup(name).Type().Underlying().(*types.Struct);for i:=0;i<t.NumFields();i++{f:=t.Field(i);fields[name+"."+f.Name()]=render(f.Type())}}
 json.NewEncoder(os.Stdout).Encode(fields)
 fs:=token.NewFileSet();out:=map[string]any{}
 for _,n:=range []string{"admin","auth","user","gateway","protocol_capabilities"}{p:="internal/server/routes/"+n+".go";f,e:=parser.ParseFile(fs,p,nil,parser.ParseComments);if e!=nil{panic(e)};decls:=map[string][2]int{};for _,d:=range f.Decls{if fn,ok:=d.(*ast.FuncDecl);ok{decls[fn.Name.Name]=[2]int{fs.Position(fn.Pos()).Offset,fs.Position(fn.End()).Offset}}};out[p]=decls}
 b,_:=json.MarshalIndent(out,"","  ");os.WriteFile(filepath.Join("/tmp","s15-route-positions.json"),b,0600)
}
