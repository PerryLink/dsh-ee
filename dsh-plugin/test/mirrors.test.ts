import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { test } from "node:test";
import { DependencyMirrors } from "../src/mirrors.js";
import { registerSkills } from "../src/skills.js";
import { normalizeConfig } from "../src/config.js";
import { runLocalDoctor } from "../src/doctor.js";

test("mirrors: private escaped files, unchanged writes, env ownership, absent and empty catalogs", async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "ee mirrors "));
  const keys = ["DSH_HOME", "PIP_CONFIG_FILE", "PIP_INDEX_URL", "PIP_EXTRA_INDEX_URL", "MAVEN_ARGS"];
  const original = Object.fromEntries(keys.map((key) => [key, process.env[key]]));
  process.env.PIP_CONFIG_FILE = "original.conf";
  process.env.PIP_INDEX_URL = "https://original/simple";
  process.env.PIP_EXTRA_INDEX_URL = "https://extra/simple";
  process.env.MAVEN_ARGS = "-Doriginal=true";
  process.env.DSH_HOME = dir;
  const ctx = { get: () => ({ dir: path.join(dir, "profile") }) } as any;
  const manager = new DependencyMirrors(ctx);
  const payload = { pypi: { url: "https://pypi.corp/simple", username: "a@b", secret: "p:/@ %" }, maven: { url: "https://maven.corp/?a=1&b=2", username: "a<&\"'", secret: "p>&\"'" } };
  try {
    manager.apply();
    assert.equal(fs.existsSync(path.join(dir,"profile")), false);
    manager.apply(payload);
    const pip = path.join(dir,"profile/dsh-ee/pip.conf");
    const maven = path.join(dir,"profile/dsh-ee/settings.xml");
    assert.equal(fs.readFileSync(pip,"utf8"), "[global]\nindex-url = https://a%40b:p%3A%2F%40%20%25@pypi.corp/simple\n");
    assert.match(fs.readFileSync(maven,"utf8"), /<mirrorOf>\*<\/mirrorOf>/);
    assert.match(fs.readFileSync(maven,"utf8"), /a=1&amp;b=2/);
    assert.match(fs.readFileSync(maven,"utf8"), /<username>a&lt;&amp;&quot;&apos;<\/username>/);
    assert.match(fs.readFileSync(maven,"utf8"), /<password>p&gt;&amp;&quot;&apos;<\/password>/);
    assert.equal(process.env.PIP_CONFIG_FILE,pip);
    assert.equal(process.env.PIP_INDEX_URL,undefined);
    assert.equal(process.env.PIP_EXTRA_INDEX_URL,undefined);
    assert.equal(process.env.MAVEN_ARGS,`-s "${maven}" -Doriginal=true`);
    if (process.platform !== "win32") {
      assert.equal(fs.statSync(path.dirname(pip)).mode & 0o777,0o700);
      for (const file of [pip,maven]) assert.equal(fs.statSync(file).mode & 0o777,0o600);
    }
    fs.utimesSync(pip,1,1);
    manager.apply(payload);
    assert.equal(fs.statSync(pip).mtimeMs,1000);
    manager.apply({ ...payload, pypi: { ...payload.pypi, url: "http://pypi.corp:8080/simple" } });
    assert.match(fs.readFileSync(pip,"utf8"), /\ntrusted-host = pypi\.corp\n$/);
    manager.apply(payload);
    assert.equal(fs.readFileSync(pip,"utf8").includes("trusted-host"),false);
    manager.apply();
    assert.equal(manager.hosts().length,2);
    const doctor = await runLocalDoctor(ctx, normalizeConfig({base:"https://ee.corp"}), {resolve:async()=>undefined} as any, {} as any, {} as any, undefined, manager);
    assert.deepEqual(doctor.checks.filter((c)=>c.id.startsWith("mirror.")).map((c)=>c.detail),["pypi.corp","maven.corp"]);
    assert.equal(JSON.stringify(doctor).includes(payload.maven.secret),false);
    manager.apply({maven:payload.maven});
    assert.equal(fs.existsSync(pip),false);
    assert.equal(process.env.PIP_INDEX_URL,"https://original/simple");
    assert.equal(process.env.PIP_EXTRA_INDEX_URL,"https://extra/simple");
    assert.equal(process.env.PIP_CONFIG_FILE,"original.conf");
    manager.apply({});
    assert.equal(fs.existsSync(maven),false);
    assert.equal(process.env.MAVEN_ARGS,"-Doriginal=true");
    manager.apply(payload);
    manager.dispose();
    assert.equal(process.env.PIP_CONFIG_FILE,"original.conf");
    assert.equal(process.env.MAVEN_ARGS,"-Doriginal=true");
    manager.apply(payload);
    assert.equal(manager.hosts().length,0);
    delete process.env.PIP_CONFIG_FILE;
    delete process.env.MAVEN_ARGS;
    const fallback = new DependencyMirrors({get:()=>undefined} as any);
    fallback.apply({pypi:payload.pypi,maven:{url:"https://maven.corp"}});
    assert.equal(process.env.PIP_CONFIG_FILE,path.join(dir,"dsh-ee/pip.conf"));
    assert.equal(fs.readFileSync(path.join(dir,"dsh-ee/settings.xml"),"utf8").includes("<servers>"),false);
    fallback.dispose();
    assert.equal(process.env.PIP_CONFIG_FILE,undefined);
    assert.equal(process.env.MAVEN_ARGS,undefined);
    const invalid = new DependencyMirrors(ctx);
    assert.throws(()=>invalid.apply({pypi:{url:"ftp://corp"}}));
    assert.throws(()=>invalid.apply({pypi:{url:"https://user:pass@corp"}}));
    fs.rmSync(pip);
    fs.symlinkSync(path.join(dir,"target"),pip);
    assert.throws(()=>invalid.apply(payload));
    invalid.dispose();
  } finally {
    manager.dispose();
    for (const [key,value] of Object.entries(original)) { if(value===undefined) delete process.env[key]; else process.env[key]=value; }
    fs.rmSync(dir,{recursive:true,force:true});
  }
});

test("catalog polling applies mirrors, leaves absent payload alone, retries failed application", async () => {
  let provider: any;
  const abort = new AbortController();
  const ctx = { get:()=>undefined, skills:{ registerProvider(factory:any) { provider=factory({invalidate(){},signal:abort.signal}); } } };
  let body:any = {skills:[],mirrors:{pypi:{url:"https://corp/simple"}}};
  const received:any[]=[];
  let fail=false;
  const mirror = {apply(value:any) { if(fail) throw new Error("disk full"); received.push(value); }};
  const http = {fetch:async()=>new Response(JSON.stringify(body),{headers:{etag:"new"}})};
  try {
    const skills = await registerSkills(ctx as any,normalizeConfig({base:"https://ee.corp"}),http as any,mirror as any);
    await skills.refreshNow();
    assert.deepEqual(received.at(-1),body.mirrors);
    body={skills:[]};
    await skills.refreshNow();
    assert.equal(received.at(-1),undefined);
    body={skills:[],mirrors:{}};
    fail=true;
    await skills.refreshNow();
    fail=false;
    await skills.refreshNow();
    assert.deepEqual(received.at(-1),{});
    assert.ok(provider);
  } finally { abort.abort(); }
});
