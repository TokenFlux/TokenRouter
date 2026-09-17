package main
import("encoding/json";"go/ast";"go/parser";"go/token";"go/printer";"bytes";"os";"path/filepath";"path";"strings";"strconv";"sort")
type route struct { Method string; Path string; File string; Function string; Handler string }
func expr(n ast.Node)string{var b bytes.Buffer;printer.Fprint(&b,token.NewFileSet(),n);return b.String()}
func literal(n ast.Expr)(string,bool){l,ok:=n.(*ast.BasicLit);if !ok||l.Kind!=token.STRING{return "",false};s,e:=strconv.Unquote(l.Value);return s,e==nil}
func walk(block *ast.BlockStmt, env map[string]string, file,fn string, result *[]route){
 local:=map[string]string{};for k,v:=range env{local[k]=v}
 for _,statement:=range block.List{
  switch s:=statement.(type){
  case *ast.AssignStmt:
   for i,rhs:=range s.Rhs{call,ok:=rhs.(*ast.CallExpr);if !ok||i>=len(s.Lhs){continue};sel,ok:=call.Fun.(*ast.SelectorExpr);if !ok||sel.Sel.Name!="Group"||len(call.Args)==0{continue};base,ok:=local[expr(sel.X)];if !ok{continue};relative,ok:=literal(call.Args[0]);if ok{local[expr(s.Lhs[i])]=path.Join(base,relative)}}
  case *ast.ExprStmt:
   call,ok:=s.X.(*ast.CallExpr);if !ok{continue};sel,ok:=call.Fun.(*ast.SelectorExpr);if !ok||!strings.Contains(" GET POST PUT DELETE PATCH HEAD OPTIONS ANY "," "+sel.Sel.Name+" ")||len(call.Args)==0{continue}
   relative,ok:=literal(call.Args[0]);if !ok{continue};base,ok:=local[expr(sel.X)];if !ok{base="UNRESOLVED:"+expr(sel.X)};full:=path.Join(base,relative);if strings.HasSuffix(relative,"/")&&!strings.HasSuffix(full,"/"){full+="/"};handler:="";if len(call.Args)>1{handler=expr(call.Args[len(call.Args)-1])};*result=append(*result,route{sel.Sel.Name,full,file,fn,handler})
  case *ast.BlockStmt:walk(s,local,file,fn,result)
  case *ast.IfStmt:walk(s.Body,local,file,fn,result);if b,ok:=s.Else.(*ast.BlockStmt);ok{walk(b,local,file,fn,result)}
  case *ast.RangeStmt:walk(s.Body,local,file,fn,result)
  case *ast.ForStmt:walk(s.Body,local,file,fn,result)
  }
 }
}
func main(){var result []route
 for _,file:=range os.Args[1:]{f,e:=parser.ParseFile(token.NewFileSet(),file,nil,0);if e!=nil{panic(e)};for _,d:=range f.Decls{fn,ok:=d.(*ast.FuncDecl);if !ok||fn.Body==nil{continue};env:=map[string]string{"r":"/","v1":"/api/v1","admin":"/api/v1/admin","authenticated":"/api/v1","group":"/api/v1/settings","auth":"/api/v1/auth","accounts":"/api/v1/admin/accounts","gateway":"/v1","adminSettings":"/api/v1/admin/settings"};walk(fn.Body,env,filepath.ToSlash(file),fn.Name.Name,&result)}}
 sort.Slice(result,func(i,j int)bool{if result[i].Path==result[j].Path{return result[i].Method<result[j].Method};return result[i].Path<result[j].Path});json.NewEncoder(os.Stdout).Encode(result)
}
