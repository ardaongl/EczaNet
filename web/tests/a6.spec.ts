import {test,expect} from '@playwright/test';
for(const role of ['seed.yildiz','teknisyen','depo','mudur','muhasebe'])for(const width of [390,1280]){
 test(`A6 live summary ${role} ${width}`,async({page})=>{
 await page.setViewportSize({width,height:900});await page.goto('/');await page.getByLabel('E-posta',{exact:true}).fill(role+'@ddo-case.invalid');await page.getByLabel('Parola',{exact:true}).fill(process.env.DEMO_PASSWORD??'Demo-EczaNet-2026!');await page.getByRole('button',{name:'Giriş yap',exact:true}).click();
 const summary=page.getByRole('region',{name:'Canlı özet'});await expect(summary.getByRole('heading',{name:'Canlı çalışma özeti'})).toBeVisible();await expect(summary).toContainText('Son güncelleme');expect(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth)).toBe(true);
 await page.route('**/api/v1/dashboard',r=>r.fulfill({status:503,json:{error:{code:'UNAVAILABLE',message:'test'}}}));await expect(summary.getByRole('alert')).toContainText('Bağlantı yok',{timeout:11000});await page.unroute('**/api/v1/dashboard');await expect(summary.getByRole('alert')).toHaveCount(0,{timeout:11000});
 });
}
