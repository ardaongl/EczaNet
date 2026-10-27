import {useState,type ReactNode} from 'react';
type Row=Record<string,unknown>;
type Field={label:string;keys:string[];format?:'date'|'money'|'status'};
const field=(label:string,...keys:string[]):Field=>({label,keys});
const fdate=(label:string,...keys:string[]):Field=>({label,keys,format:'date'});
const money=(label:string,...keys:string[]):Field=>({label,keys,format:'money'});
const status=field('Durum','status');status.format='status';
const layouts:Record<string,Field[]>={
 'Satışlar':[fdate('İşlem tarihi','effective_at'),money('Toplam','total'),money('Hasta payı','patient_total')],
 'İadeler':[fdate('İşlem tarihi','effective_at'),field('Kaynak satış','sale_id'),money('Hasta payı','patient_total')],
 'Kontrollü defter':[fdate('İşlem tarihi','effective_at','business_day'),field('Ürün','product_id'),field('Miktar','quantity')],
 'Transfer':[status,field('Kaynak','source_id'),field('Hedef','target_id')],
 'Sayım':[status,field('Ürün','product_id'),fdate('Son geçerlilik','expires_at')],
 'Karantina':[field('Lot','lot'),field('Kalan','remaining'),fdate('Son kullanma','expires_on')],
 'İhtiyaçlar':[status,field('Ürün','product_id'),field('Miktar','quantity')],
 'Siparişler':[status,field('Tedarikçi','supplier_id'),field('Lokasyon','location_id')],
 'Tahsisler':[field('Sipariş','order_id'),field('Ürün','product_id'),field('Yolda','in_transit')],
 'Öneriler':[field('Önerilen miktar','suggested'),field('Etkin stok','effective'),field('Lokasyon','location_id')],
 'Kritik stok':[field('Etkin stok','effective'),field('Minimum stok','minimum'),field('Önerilen miktar','suggested')],
 'Raporlar':[field('Lot','lot'),fdate('Son kullanma','expires_on'),field('Kalan gün','days')],
 'Stok uzlaştırma':[field('Lot','lot'),field('Ürün','product_id'),field('Hesap sayısı','accounts')],
 'Transfer farkları':[status,field('Kaynak','source_id'),field('Hedef','target_id')],
 'Finans raporları':[fdate('Gün','day'),field('Satış sayısı','sale_count'),money('Net','net')],
 'Fiyat politikası':[field('Ürün','product_id'),money('Kutu fiyatı','pack_price'),fdate('Geçerlilik başlangıcı','valid_from')]
};
const statuses:Record<string,string>={DRAFT:'Taslak',APPROVED:'Onaylandı',ORDERED:'Siparişe bağlandı',OPEN:'Açık',SENT:'Sevk edildi',PARTIALLY_RECEIVED:'Kısmen teslim alındı',RECEIVED:'Teslim alındı',COMPLETED:'Tamamlandı',CANCELLED:'İptal edildi',CLOSED:'Kapandı'};
function value(row:Row,f:Field){const raw=f.keys.map(k=>row[k]).find(v=>v!==null&&v!==undefined&&v!=='');if(raw===undefined)return '—';if(Array.isArray(raw))return String(raw.length);if(f.format==='status')return statuses[String(raw)]??String(raw);if(f.format==='date'){const s=String(raw);const d=new Date(s.length===10?s+'T12:00:00Z':s);return Number.isNaN(d.getTime())?s:new Intl.DateTimeFormat('tr-TR',s.length===10?{dateStyle:'short',timeZone:'Europe/Istanbul'}:{dateStyle:'short',timeStyle:'short',timeZone:'Europe/Istanbul'}).format(d)}if(f.format==='money'){const n=Number(raw);return Number.isSafeInteger(n)?`${new Intl.NumberFormat('tr-TR',{minimumFractionDigits:2,maximumFractionDigits:2}).format(n/100)} TL`:String(raw)+' kuruş'}return String(raw)}
export function RecordList({page,items,renderDetails}:{page:string;items:Row[];renderDetails:(row:Row)=>ReactNode}){
 const [query,setQuery]=useState(''),[selected,setSelected]=useState('all');const fields=layouts[page]??[status,field('Ürün','product_id'),field('Miktar','quantity')];
 const statusesPresent=Array.from(new Set(items.map(row=>row.status).filter((v):v is string=>typeof v==='string'&&!!v)));
 const filtered=items.filter(row=>(selected==='all'||row.status===selected)&&JSON.stringify(row).toLocaleLowerCase('tr-TR').includes(query.trim().toLocaleLowerCase('tr-TR')));
 return <section className="list-panel" aria-label={`${page} listesi`}><div className="list-toolbar"><div><h2>{page} kayıtları</h2><p>Bir kaydı açarak ayrıntıları ve kullanılabilir işlemleri görüntüleyin.</p></div><div className="record-filters"><label>Bu sayfada ara<input value={query} onChange={e=>setQuery(e.target.value)} placeholder="Kayıtlarda ara"/></label>{statusesPresent.length>0&&<label>Bu sayfada durum<select value={selected} onChange={e=>setSelected(e.target.value)}><option value="all">Tüm durumlar</option>{statusesPresent.map(v=><option key={v} value={v}>{statuses[v]??v}</option>)}</select></label>}</div></div><p className="list-count">Bu sayfada {filtered.length} / {items.length} kayıt gösteriliyor. Arama ve filtre yalnızca yüklenen sayfaya uygulanır.</p>{filtered.length===0?<p className="list-empty">Bu sayfada aramaya veya filtreye uygun kayıt bulunamadı.</p>:<div className="record-table records"><div className="record-head" aria-hidden="true"><span>Kayıt</span>{fields.map(f=><span key={f.label}>{f.label}</span>)}</div>{filtered.map(row=><details className="record-row" key={String(row.id)}><summary><span className="record-main"><h3>{String(row.name??('Belge '+row.id))}</h3>{row.name!=null&&<small>Belge {String(row.id)}</small>}</span>{fields.map(f=><span key={f.label} data-label={f.label}>{value(row,f)}</span>)}<span className="row-expand">Ayrıntılar</span></summary><div className="record-details record-detail-body">{renderDetails(row)}</div></details>)}</div>}</section>
}
