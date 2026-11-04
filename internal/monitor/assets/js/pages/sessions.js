// 会话路由（命名会话）：把同一个 sessionID 固定到同一个上游节点。
// 对应 /api/proxy-sessions 与 /api/proxy-sessions/links，仅在 pool / hybrid 模式可用。
async function copyText(text){
  try{await navigator.clipboard.writeText(text);toast('已复制')}catch(e){window.prompt('手动复制：',text)}
}
function sessionPreview(){
  const id=qs('#sessionID')?.value||'';
  const scheme=qs('#sessionScheme')?.value||'http';
  const label=id?`${scheme}://用户名.${id}:密码@入口地址`:`${scheme}://用户名:密码@入口地址`;
  const host=qs('#sessionPreview');
  if(host)host.textContent=label;
}
function randomSessionID(){
  const chars='abcdefghijklmnopqrstuvwxyz0123456789';
  let id='';
  const bytes=new Uint8Array(8);
  (window.crypto||{}).getRandomValues?window.crypto.getRandomValues(bytes):bytes.forEach((_,i)=>bytes[i]=Math.floor(Math.random()*256));
  for(const b of bytes)id+=chars[b%chars.length];
  const input=qs('#sessionID');
  if(input){input.value=id;sessionPreview()}
}
async function loadSessions(request){
  const data=await api('/api/proxy-sessions',{signal:request?.signal});
  S.sessions=data;
}
function sessionTable(rows,enabled){
  if(!rows.length)return `<div class="connectivity-idle"><span>还没有活跃的会话绑定</span></div>`;
  return `<div class="table-wrap"><table class="table"><thead><tr><th>会话 ID</th><th>绑定节点</th><th>最近使用</th><th>到期</th><th>切换</th><th></th></tr></thead><tbody>${
    rows.map(row=>`<tr>
      <td class="mono">${esc(row.id)}</td>
      <td>${esc(row.node_name||row.node_tag||row.node_id||'-')}</td>
      <td>${esc(relativeTime(row.last_seen))}</td>
      <td>${esc(relativeTime(row.expires_at))}</td>
      <td>${esc(Number(row.switch_count)||0)}</td>
      <td class="row-actions"><button class="btn small secondary" type="button" ${enabled?'':'disabled'} onclick="resetSession('${esc(row.id)}')">重置</button></td>
    </tr>`).join('')}</tbody></table></div>`;
}
function renderSessions(){
  const data=S.sessions||{};
  const rows=Array.isArray(data.sessions)?data.sessions:[];
  const enabled=!!data.enabled;
  const reason=data.reason||'';
  qs('#view').innerHTML=`
  <div class="card">
    <div class="card-head"><div><h2>生成会话链接</h2><p class="muted">用「用户名.sessionID」访问共享入口，同一会话固定同一上游节点；普通用户名沿用池调度。</p></div></div>
    ${enabled?'':`<div class="empty warn"><strong>当前不可用</strong><span>${esc(reason||'请先在设置中开启会话路由并配置共享入口用户名')}</span></div>`}
    <div class="form-grid">
      <div class="field"><label for="sessionID">会话 ID</label><input id="sessionID" maxlength="100" autocomplete="off" placeholder="例如 alice01" oninput="sessionPreview()"></div>
      <div class="field"><label for="sessionScheme">协议</label><select id="sessionScheme" onchange="sessionPreview()"><option value="http">HTTP</option><option value="socks5">SOCKS5</option></select></div>
      <div class="field actions"><button class="btn secondary" type="button" onclick="randomSessionID()">随机生成</button><button class="btn" type="button" onclick="generateSessionLink()">生成链接</button></div>
    </div>
    <div class="connectivity-idle"><span id="sessionPreview" class="mono"></span></div>
    <div id="sessionLinks" class="links-box"></div>
  </div>
  <div class="card">
    <div class="card-head"><div><h2>活跃会话</h2><p class="muted">共 ${Number(data.total)||0} 个绑定 · TTL ${esc(data.ttl||'-')} · 上限 ${Number(data.max_sessions)||0}</p></div>
      <div class="card-actions"><button class="btn secondary danger" type="button" ${enabled&&rows.length?'':'disabled'} onclick="resetAllSessions()">重置全部</button></div></div>
    ${sessionTable(rows,enabled)}
  </div>`;
  sessionPreview();
}
async function generateSessionLink(){
  const id=(qs('#sessionID')?.value||'').trim();
  const scheme=qs('#sessionScheme')?.value||'http';
  try{
    const data=await api('/api/proxy-sessions/links',{method:'POST',body:JSON.stringify({session_id:id,scheme})});
    const curl=`curl -x ${data.url.replace(/\/\/[^@]*@/,'//用户名:密码@')} https://www.gstatic.com/generate_204`;
    qs('#sessionLinks').innerHTML=`
      <div class="link-row"><span class="mono">${esc(data.masked_url||'')}</span>
        <button class="btn small secondary" type="button" onclick="copyText(${JSON.stringify(data.url||'')})">复制完整链接</button>
        <button class="btn small secondary" type="button" onclick="copyText(${JSON.stringify(curl)})">复制 curl</button></div>
      <p class="muted">完整链接包含真实密码，仅在此处显示；不会创建绑定，首次请求时生效。</p>`;
    toast('已生成会话链接');
  }catch(err){toast(err.message,'err')}
}
async function resetSession(id){
  try{
    await api('/api/proxy-sessions/'+encodeURIComponent(id),{method:'DELETE'});
    toast('会话已重置');
    await refresh();
  }catch(err){toast(err.message,'err')}
}
async function resetAllSessions(){
  if(!confirm('确定重置全部会话绑定？'))return;
  try{
    const data=await api('/api/proxy-sessions',{method:'DELETE'});
    toast(data.message||'已重置全部会话');
    await refresh();
  }catch(err){toast(err.message,'err')}
}
