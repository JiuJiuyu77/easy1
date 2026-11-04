// 诊断页：上游旧面板的「调试」能力——每个节点的成功率、最后错误、时间线，
// 以及黑名单节点的解封/拉黑、单节点立即探测（/api/debug、/api/nodes）。
async function loadDiagnostics(request){
  const [debug,diagnostics]=await Promise.all([
    api('/api/debug',{signal:request?.signal}),
    api('/api/diagnostics',{signal:request?.signal}).catch(err=>{if(isAbortError(err))throw err;return null})
  ]);
  S.debug=debug;S.diagnostics=diagnostics;
}
function debugNodeRow(n){
  const calls=(Number(n.success_count)||0)+(Number(n.failure_count)||0);
  const rate=calls?Math.round((Number(n.success_count)||0)*100/calls):0;
  const latency=Number(n.last_latency_ms)>0?`${n.last_latency_ms} ms`:'-';
  const status=n.blacklisted?'<span class="pill red">已拉黑</span>':n.available?'<span class="pill green">正常</span>':'<span class="pill amber">异常</span>';
  const timeline=(n.timeline||[]).slice(-12).map(e=>`<span class="tl-dot ${e.success?'ok':'bad'}" title="${esc((e.success?'成功':'失败')+(e.latency_ms?` ${e.latency_ms}ms`:''))}${e.error?' · '+esc(e.error):''}"></span>`).join('');
  return `<div class="table-row debug-row">
    <span class="mono" title="${esc(n.tag)}">${esc(n.name||n.tag)}</span>
    <span>${status}</span>
    <span class="muted">${esc(latency)}</span>
    <span class="muted">${calls?rate+'%':'—'}<small class="muted"> (${Number(n.success_count)||0}/${calls})</small></span>
    <span class="muted debug-error" title="${esc(n.last_error||'')}">${esc(n.last_error||'-')}</span>
    <span class="tl-strip">${timeline||'<span class="muted">-</span>'}</span>
    <span class="row-actions">
      <button class="btn small secondary" type="button" onclick="probeDebugNode('${esc(n.tag)}')">探测</button>
      ${n.blacklisted
        ? `<button class="btn small green" type="button" onclick="releaseDebugNode('${esc(n.tag)}')">解封</button>`
        : `<button class="btn small danger" type="button" onclick="blacklistDebugNode('${esc(n.tag)}')">拉黑</button>`}
    </span>
  </div>`;
}
function renderDiagnostics(){
  const debug=S.debug||{},runtime=S.diagnostics?.runtime||{},go=S.diagnostics?.go||{};
  const nodes=Array.isArray(debug.nodes)?debug.nodes:[];
  const blacklisted=nodes.filter(n=>n.blacklisted);
  qs('#view').innerHTML=`
  <div class="port-stats">
    <div class="metric"><span>节点</span><strong>${nodes.length}</strong></div>
    <div class="metric"><span>成功率</span><strong class="${(Number(debug.success_rate)||0)>=80?'good':'bad'}">${nodes.length?(Math.round((Number(debug.success_rate)||0)*10)/10)+'%':'—'}</strong></div>
    <div class="metric"><span>调用/成功</span><strong>${Number(debug.total_calls)||0} / ${Number(debug.total_success)||0}</strong></div>
    <div class="metric"><span>黑名单</span><strong class="${blacklisted.length?'bad':'good'}">${blacklisted.length}</strong></div>
  </div>
  <div class="card">
    <div class="card-head"><div><h2>节点诊断</h2><p class="muted">成功率按进程内调用统计；时间线是最近 12 次探测结果。</p></div>
      <button class="btn small secondary" type="button" onclick="refresh()">${icon('refresh','sm')}刷新</button></div>
    <div class="table debug-list">
      <div class="table-head debug-row"><span>节点</span><span>状态</span><span>延迟</span><span>成功率</span><span>最后错误</span><span>时间线</span><span></span></div>
      ${nodes.length?nodes.map(debugNodeRow).join(''):'<div class="empty">还没有节点：先在「导入节点」里导入并加入池</div>'}
    </div>
  </div>
  <div class="card">
    <div class="card-head"><h2>运行时</h2></div>
    <div class="card-body"><div class="kv-grid">
      <div><span>代理核心</span><strong class="${runtime.running?'ok':'bad'}">${runtime.running?'运行中':'未运行'}</strong></div>
      <div><span>模式</span><strong>${esc(runtime.mode||'-')}</strong></div>
      <div><span>配置节点</span><strong>${Number(runtime.configured_nodes)||0}</strong></div>
      <div><span>监听端口</span><strong>${Number(runtime.listeners)||0}</strong></div>
      <div><span>协程</span><strong>${Number(go.goroutines)||0}</strong></div>
      <div><span>堆内存</span><strong>${formatHeap(go.heap_alloc_bytes)}</strong></div>
      <div><span>GC 次数</span><strong>${Number(go.gc_count)||0}</strong></div>
    </div></div>
  </div>`;
}
async function probeDebugNode(tag){
  try{const r=await api('/api/nodes/'+encodeURIComponent(tag)+'/probe',{method:'POST'});toast(`探测完成${r&&r.latency_ms?`，延迟 ${r.latency_ms} ms`:''}`);await refresh()}catch(err){toast(err.message,'err')}
}
async function releaseDebugNode(tag){
  try{await api('/api/nodes/'+encodeURIComponent(tag)+'/release',{method:'POST'});toast('已解封');await refresh()}catch(err){toast(err.message,'err')}
}
async function blacklistDebugNode(tag){
  if(!confirm(`拉黑节点「${tag}」？该节点会立即停止被调度，直到手动解封或冷却结束。`))return;
  try{await api('/api/nodes/'+encodeURIComponent(tag)+'/blacklist',{method:'POST'});toast('已拉黑');await refresh()}catch(err){toast(err.message,'err')}
}
