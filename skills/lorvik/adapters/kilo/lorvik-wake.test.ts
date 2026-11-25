// SPDX-License-Identifier: Apache-2.0
import { afterEach, expect, test } from "bun:test";
import { spawn } from "node:child_process";
import { existsSync, mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import plugin, { createLorvikWake } from "./lorvik-wake";
import { sessionLabel } from "./identity";

const original={...process.env};
const cleanups:Array<()=>Promise<void>>=[];
afterEach(async()=>{
  for(const cleanup of cleanups.splice(0))await cleanup();
  for(const key of Object.keys(process.env))if(!(key in original))delete process.env[key];
  Object.assign(process.env,original);
});
const delay=(ms:number)=>new Promise(resolve=>setTimeout(resolve,ms));
async function until(check:()=>boolean){
  for(let i=0;i<100;i++){if(check())return;await delay(20);}
  throw new Error("timed out waiting for adapter boundary");
}
async function fixture(mode="events", pluginUnderTest=plugin) {
  const root=mkdtempSync(join(tmpdir(),"lorvik-kilo-check-"));
  mkdirSync(join(root,"bin"));writeFileSync(join(root,".lorvik.toml"),"");
  const log=join(root,"invocation");
  writeFileSync(join(root,"bin","lorvik"),`#!/bin/sh
printf '%s\\n' "$@" "$LORVIK_SESSION" "$LORVIK_CONFIG" >> "$LORVIK_KILO_FIXTURE_LOG"
case "$LORVIK_KILO_FIXTURE_MODE" in
events) printf '%s' 'now ana commented on #42 "literal $(touch NEVER)"'; printf '\\nsecond event\\n';;
failure) printf 'fixture authentication rejected\\n' >&2; exit 7;;
delayed) trap '' TERM; /bin/sleep 0.4 & wait $!; exit;;
ordered) printf 'first batch\\n'; /bin/sleep 0.35; printf 'second batch\\n';;
selfordered) printf 'first batch\\n'; /bin/sleep 0.35; printf 'final batch\\n'; exit;;
deadline) printf 'deadline batch\\n';;
overflow) head -c 1100000 /dev/zero | tr '\\0' x; printf '\\nnewest complete event\\n';;
complete) i=0; while [ "$i" -lt 20 ]; do head -c 70000 /dev/zero | tr '\\0' x; printf ' line%s\\n' "$i"; i=$((i+1)); done;;
partial) printf 'short unterminated event'; exit;;
esac
exec /bin/sleep 30
`,{mode:0o700});
  process.env.PATH=`${join(root,"bin")}:${original.PATH}`;
  process.env.LORVIK_CONFIG=join(root,"agent.toml");
  process.env.LORVIK_KILO_FIXTURE_LOG=log;
  process.env.LORVIK_KILO_FIXTURE_MODE=mode;
  let route:any={name:"session",params:{sessionID:"ses_fixture"}};
  const calls:any[]=[],toasts:any[]=[];const releases:Array<()=>void>=[];let commands:any[]=[];let dispose=async()=>{};
  const lifetime=new AbortController();
  const api:any={
    get route(){return {get current(){return route;}};},
    lifecycle:{signal:lifetime.signal,onDispose(fn:any){dispose=fn;return ()=>{};}},
    state:{path:{directory:root}},
    ui:{toast(input:any){toasts.push(input);},dialog:{clear(){}}},
    command:{register(fn:any){commands=fn();return ()=>{};}},
    client:{v2:{session:{async prompt(input:any,options:any){
      calls.push({input,options});
      if(mode==="deadline") return new Promise(()=>{});
      if(mode!=="ordered"&&mode!=="selfordered") return {data:{data:{id:"msg_fixture"}}};
      return new Promise(resolve=>releases.push(()=>resolve({data:{data:{id:"msg_fixture"}}})));
    }}}},
  };
  await pluginUnderTest.tui(api);
  cleanups.push(async()=>{lifetime.abort();await dispose();rmSync(root,{recursive:true,force:true});});
  return {calls,toasts,log,root,dispose,release(){releases.shift()?.();},changeRoute(){route={name:"home"};},
    async command(value:string){await commands.find(c=>c.value===value).onSelect();}};
}

test("explicit arm starts one correctly labelled listener; batches literal lines into Kilo v2 queue prompt",async()=>{
  const f=await fixture();
  expect(f.calls).toHaveLength(0);
  await f.command("lorvik.listen");await f.command("lorvik.listen");
  await until(()=>f.calls.length===1);
  const label=sessionLabel("ses_fixture");
  expect(readFileSync(f.log,"utf8").split("\n")).toEqual(["--session",label,"inbox","--follow","--exit-on-eof",label,process.env.LORVIK_CONFIG!,""]);
  expect(f.calls[0].input).toMatchObject({sessionID:"ses_fixture",delivery:"queue",resume:true});
  expect(f.calls[0].input.prompt.text).toContain('literal $(touch NEVER)');
  expect(f.calls[0].input.prompt.text).toContain("\nsecond event\n");
  expect(f.calls[0].input.prompt.text).toContain(`lorvik --session ${label} inbox`);
  await f.command("lorvik.unlisten");
},5000);

test("quiet board creates no prompt; navigation disarms and never automatically rearms",async()=>{
  const f=await fixture("quiet");await f.command("lorvik.listen");await until(()=>f.toasts.length===1);
  await delay(350);expect(f.calls).toHaveLength(0);
  f.changeRoute();await delay(250);expect(f.calls).toHaveLength(0);
  await f.command("lorvik.listen");expect(f.toasts.at(-1).message).toContain("Open a chat");
},5000);

test("listener self-exit reports once and never silently restarts",async()=>{
  const f=await fixture("failure");await f.command("lorvik.listen");
  await until(()=>f.toasts.some(t=>t.message.includes("fixture authentication rejected")));
  await delay(300);
  expect(f.toasts.filter(t=>t.message.includes("Listener stopped"))).toHaveLength(1);
  expect(readFileSync(f.log,"utf8").split("--follow")).toHaveLength(2);
  expect(f.calls).toHaveLength(0);
},5000);

test("disposal discards a pending board batch before injection",async()=>{
  const f=await fixture();await f.command("lorvik.listen");await delay(40);await f.dispose();await delay(300);
  expect(f.calls).toHaveLength(0);
},5000);

test("a concurrent unlisten and listen cannot overlap listeners during TERM grace",async()=>{
  const f=await fixture("delayed");
  await f.command("lorvik.listen");
  await until(()=>existsSync(f.log)&&readFileSync(f.log,"utf8").includes("--follow"));
  const unlisten=f.command("lorvik.unlisten");
  await delay(30);
  const listen=f.command("lorvik.listen");
  await delay(120);
  expect(readFileSync(f.log,"utf8").split("--follow")).toHaveLength(2);
  await Promise.all([unlisten,listen]);
  await f.command("lorvik.listen");
  await until(()=>readFileSync(f.log,"utf8").split("--follow").length===3);
  await f.command("lorvik.unlisten");
},5000);

test("serializes two batches in listener order",async()=>{
  const f=await fixture("ordered");
  await f.command("lorvik.listen");
  await until(()=>f.calls.length===1);
  await delay(450);expect(f.calls).toHaveLength(1);
  f.release();
  await until(()=>f.calls.length===2);
  expect(f.calls[0].input.prompt.text).toContain("first batch");
  expect(f.calls[1].input.prompt.text).toContain("second batch");
  await f.command("lorvik.unlisten");
},5000);

test("disarm drops a later batch waiting behind an admitted request",async()=>{
  const f=await fixture("ordered");
  await f.command("lorvik.listen");
  await until(()=>f.calls.length===1);
  await delay(450);await f.command("lorvik.unlisten");
  f.release();await delay(100);
  expect(f.calls).toHaveLength(1);
},5000);

test("self-exit keeps the final delivery cancellable until explicit disarm",async()=>{
  const f=await fixture("selfordered");
  await f.command("lorvik.listen");
  await until(()=>f.calls.length===1);
  await until(()=>f.toasts.some(toast=>toast.message.includes("Listener stopped")));
  await f.command("lorvik.listen");
  expect(readFileSync(f.log,"utf8").split("--follow")).toHaveLength(2);
  await f.command("lorvik.unlisten");
  expect(f.calls[0].options.signal.aborted).toBe(true);
  f.release();await delay(100);
  expect(f.calls).toHaveLength(1);
  await f.command("lorvik.listen");
  await until(()=>readFileSync(f.log,"utf8").split("--follow").length===3);
  await f.command("lorvik.unlisten");
},5000);

test("a deadline disarms even when the Kilo request ignores AbortSignal",async()=>{
  let expire=()=>{};
  const f=await fixture("deadline",createLorvikWake({
    setTimeout(callback){expire=callback;return 1 as any;},
    clearTimeout(){},
  }));
  await f.command("lorvik.listen");
  await until(()=>f.calls.length===1);
  expire();
  await until(()=>f.toasts.some(toast=>toast.message.includes("timed out")));
  expect(f.calls[0].options.signal.aborted).toBe(true);
  await delay(100);expect(f.calls).toHaveLength(1);
},5000);

test("keeps newest complete records and reports overflow once",async()=>{
  const f=await fixture("overflow");
  await f.command("lorvik.listen");
  await until(()=>f.calls.length===1);
  expect(f.calls[0].input.prompt.text).toContain("newest complete event");
  expect(f.calls[0].input.prompt.text).toContain("1 earlier notification line(s) were dropped");
  await f.command("lorvik.unlisten");
},5000);

test("coalesces complete overflow records within the rendered 1 MiB budget",async()=>{
  const f=await fixture("complete");
  await f.command("lorvik.listen");
  await until(()=>f.calls.length===1);
  const text=f.calls[0].input.prompt.text;
  expect(text).toContain("line19");
  expect(text).toContain("notification line(s) were dropped");
  expect(Buffer.byteLength(text,"utf8")).toBeLessThanOrEqual(1024*1024);
  await f.command("lorvik.unlisten");
},5000);

test("EOF drops an unterminated short record and sends one recovery notice",async()=>{
  const f=await fixture("partial");
  await f.command("lorvik.listen");
  await until(()=>f.calls.length===1);
  expect(f.calls[0].input.prompt.text).not.toContain("short unterminated event");
  expect(f.calls[0].input.prompt.text).toContain("1 earlier notification line(s) were dropped");
  await f.command("lorvik.unlisten");
},5000);

test("an abruptly killed adapter owner closes the listener stdin pipe",async()=>{
  const root=mkdtempSync(join(tmpdir(),"lorvik-kilo-eof-"));
  mkdirSync(join(root,"bin"));writeFileSync(join(root,".lorvik.toml"),"");
  const argv=join(root,"argv"),eof=join(root,"eof"),pid=join(root,"listener.pid"),host=join(root,"owner.ts");
  const adapter=new URL("./lorvik-wake.ts",import.meta.url).href;
  writeFileSync(join(root,"bin","lorvik"),`#!/bin/sh
printf '%s\\n' "$@" >> "$LORVIK_KILO_EOF_ARGV"
printf '%s\\n' "$$" > "$LORVIK_KILO_EOF_PID"
cat >/dev/null
printf eof > "$LORVIK_KILO_EOF"
`,{mode:0o700});
  writeFileSync(host,`import plugin from ${JSON.stringify(adapter)};
let commands=[];const life=new AbortController();
const api={get route(){return {get current(){return {name:"session",params:{sessionID:"ses_eof"}};}};},lifecycle:{signal:life.signal,onDispose(){return ()=>{};}},state:{path:{directory:process.cwd()}},ui:{toast(){},dialog:{clear(){}}},command:{register(fn){commands=fn();return ()=>{};}},client:{v2:{session:{async prompt(){return {data:{data:{id:"msg"}}};}}}}};
await plugin.tui(api);await commands.find((command)=>command.value==="lorvik.listen").onSelect();
setInterval(()=>{},1000);
`);
  const owner=spawn(process.execPath,[host],{cwd:root,env:{...process.env,PATH:`${join(root,"bin")}:${original.PATH}`,LORVIK_CONFIG:join(root,"agent.toml"),LORVIK_KILO_EOF_ARGV:argv,LORVIK_KILO_EOF:eof,LORVIK_KILO_EOF_PID:pid},stdio:["ignore","ignore","ignore"]});
  cleanups.push(async()=>{owner.kill("SIGKILL");rmSync(root,{recursive:true,force:true});});
  await until(()=>existsSync(argv)&&existsSync(pid));
  expect(existsSync(eof)).toBe(false);
  expect(process.kill(Number(readFileSync(pid,"utf8")),0)).toBe(true);
  const closed=new Promise<void>(resolve=>owner.once("close",()=>resolve()));
  owner.kill("SIGKILL");
  await closed;
  await until(()=>existsSync(eof));
  expect(readFileSync(argv,"utf8").split("\n")).toEqual(["--session",sessionLabel("ses_eof"),"inbox","--follow","--exit-on-eof",""]);
},5000);
