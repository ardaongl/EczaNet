import {expect,request,type APIRequestContext} from '@playwright/test';
import {execFileSync} from 'node:child_process';
import {fileURLToPath} from 'node:url';
const repositoryRoot=fileURLToPath(new URL('../../../',import.meta.url));
import {randomUUID} from 'node:crypto';
import {allRows,type Sale} from './a2';

export const password=process.env.DEMO_PASSWORD??'Demo-EczaNet-2026!';
export type Account={api:APIRequestContext;csrf:string};
export function db(sql:string):string{
 const project=process.env.TEST_COMPOSE_PROJECT;
 if(project&&!['eczanet-a6','eczanet-a6-ui'].includes(project))throw Error('Unknown regression project');
 const target=project?['-p',project,'-f','compose.yaml','-f',`tests/compose.${project==='eczanet-a6'?'a6':'a6-ui'}.yaml`]:[];
 return execFileSync('docker',['compose',...target,'exec','-T','db','psql','-v','ON_ERROR_STOP=1','-At','-U','eczanet_owner','-d','eczanet_demo'],{input:sql,encoding:'utf8',cwd:repositoryRoot}).trim();
}
export async function account(email:string):Promise<Account>{
 const api=await request.newContext({baseURL:process.env.TEST_WEB_URL??'http://localhost:3000'});
 const prep=await(await api.get('/api/v1/auth/csrf')).json();
 const response=await api.post('/api/v1/auth/login',{headers:{Origin:'http://localhost:3000','X-CSRF-Token':prep.csrf_token},data:{email,password}});
 expect(response.status()).toBe(200);return {api,csrf:(await response.json()).csrf_token};
}
export async function logout(a:Account){
 try{await a.api.post('/api/v1/auth/logout',{headers:{Origin:'http://localhost:3000','X-CSRF-Token':a.csrf}})}finally{await a.api.dispose()}
}
export async function post(a:Account,path:string,data:unknown,status=201){
 const response=await a.api.post('/api/v1'+path,{headers:{Origin:'http://localhost:3000','X-CSRF-Token':a.csrf,'Idempotency-Key':randomUUID()},data});
 expect(response.status(),path).toBe(status);return response.json();
}
export async function stock(warehouse:Account,receiver:Account,quantity:string,prefix:string){
 const barcode='00'+Date.now()+Math.floor(Math.random()*100000).toString().padStart(5,'0');
 const product=await post(warehouse,'/products',{location_id:'21',product:{barcode,name:prefix,dose:'5mg',form:'tablet',base_unit:'tablet',pack_size:'10',blister_size:'5',sale_multiple:'1',ingredients:['a2-regression'],prescription_required:false,controlled_group:'',pack_price:'1000'}});
 await post(warehouse,'/goods-receipts',{location_id:'21',supplier_id:'1',document_no:prefix,reason:'A2 regression fixture; retained for review',lines:[{product_id:product.id,lot:prefix,expires_on:'2099-12-31',quantity}]});
 const transfer=await post(warehouse,'/transfers',{location_id:'21',target_id:'1',items:[{product_id:product.id,quantity,unit:'unit'}]});
 await post(warehouse,'/transfers/'+transfer.id+'/send',{location_id:'21'},200);
 await post(receiver,'/transfers/'+transfer.id+'/receive',{location_id:'1'},200);
 return {product:product.id as string,lot:transfer.lines?.[0]?.lot_id as string|undefined,transfer:transfer.id as string,barcode};
}
// Fresh CI may have fewer rows. Add real, uniquely scoped fixtures; retained data is never reset.
export async function ensureSalesHistory(){
 const a=await account('seed.yildiz@ddo-case.invalid');let warehouse:Account|undefined;
 try{
  const before=await allRows<Sale>(a.api,'/api/v1/sales?location_id=1');
  if(before.length>200)return;
  warehouse=await account('depo@ddo-case.invalid');const needed=201-before.length;
  const f=await stock(warehouse,a,String(needed),'A2-PAGINATION-'+randomUUID());
  const input={location_id:'1',items:[{product_id:f.product,quantity:'1',unit:'unit'}]};
  const quote=await post(a,'/sales/quote',input,200);
  for(let i=0;i<needed;i++)await post(a,'/sales',{...input,quote:quote.quote});
  expect((await allRows<Sale>(a.api,'/api/v1/sales?location_id=1')).length).toBeGreaterThan(200);
 }finally{if(warehouse)await logout(warehouse);await logout(a)}
}
