import {expect,type APIRequestContext,type Page} from '@playwright/test';

export type Sale={id:string;total:string;lines:{product_id:string;quantity:string}[]};
export type Inventory={id:string;product_id:string;balances:Record<string,string>};
export type Transfer={id:string;status:string;lines:{lot_id:string;quantity:string;good:string;damaged:string;missing:string}[]};
export type Product={id:string;barcode:string};

// Follow the server's cursor; never assume a fixture belongs to the first page.
export async function allRows<T>(api:APIRequestContext,path:string):Promise<T[]>{
 const url=new URL(path,process.env.TEST_WEB_URL??'http://localhost:3000');url.searchParams.set('limit','200');
 const rows:T[]=[];const seen=new Set<string>();let cursor='';
 do{
  expect(seen.has(cursor),'cursor must advance').toBe(false);seen.add(cursor);
  url.searchParams.set('cursor',cursor);
  const response=await api.get(url.pathname+url.search);expect(response.status()).toBe(200);
  const result=await response.json() as {items:T[];next_cursor:string};
  rows.push(...result.items);cursor=result.next_cursor;
 }while(cursor);
 return rows;
}

// Business lists show 50 rows. Locate this fixture through the actual UI pages.
export async function findOnPages(page:Page,path:'/api/v1/transfers'|'/api/v1/inventory'|'/api/v1/controlled-ledger'|'/api/v1/orders'|'/api/v1/needs'|'/api/v1/allocations'|'/api/v1/counts',kind:'button'|'heading',name:string){
 const record=page.getByRole(kind,{name,exact:true});
 const next=page.getByRole('button',{name:'Sonraki sayfa',exact:true});
 const seen=new Set<string>();
 for(;;){
  // A successful mutation is visible before its list refresh finishes. Wait for
  // either this exact record or a next page, not just for the old page's button.
  await expect.poll(async()=>
   await page.getByRole('status').filter({hasText:'Veriler yükleniyor'}).count()===0&&
   (await record.count()>0||await next.count()>0),
   {message:'fixture or its next page must be reachable without resetting data'}
  ).toBe(true);
  if(await record.count()>0){await expect(record).toHaveCount(1);return record}
  const response=page.waitForResponse(r=>r.request().method()==='GET'&&new URL(r.url()).pathname===path&&!!new URL(r.url()).searchParams.get('cursor')&&r.status()===200);
  await next.click();const result=await response;const cursor=new URL(result.url()).searchParams.get('cursor')!;expect(seen.has(cursor),'UI cursor must advance').toBe(false);seen.add(cursor);
 }
}
