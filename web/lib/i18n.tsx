"use client";

import { createContext, useContext, useEffect, useState } from "react";

export type Lang = "zh" | "en";

// messages holds the UI string catalog. Chinese is the default; English is the
// fallback when a key is missing in the active locale.
const messages: Record<Lang, Record<string, string>> = {
  zh: {
    "nav.runs": "压测任务",
    "nav.tests": "测试用例",
    "nav.workers": "工作节点",
    "nav.users": "用户",
    "nav.signout": "退出登录",
    "login.title": "登录 loadify",
    "login.email": "邮箱",
    "login.password": "密码",
    "login.signin": "登录",
    "login.signingin": "登录中…",
    "login.feishu": "使用飞书登录",
    "login.failed": "登录失败",
    "login.feishuFailed": "飞书登录失败",
    "runs.title": "压测任务",
    "runs.start": "发起压测",
    "runs.test": "测试用例",
    "runs.selectTest": "选择一个用例…",
    "runs.workers": "工作节点数",
    "runs.startBtn": "开始压测",
    "runs.colRun": "任务",
    "runs.colStatus": "状态",
    "runs.colWorkers": "节点数",
    "runs.colStarted": "开始时间",
    "runs.empty": "暂无压测任务。",
    "run.title": "压测任务",
    "run.stop": "停止压测",
    "run.throughput": "吞吐 (QPS)",
    "run.latency": "延迟 (ms)",
    "run.errorRate": "错误率 (%)",
    "run.summary": "汇总",
    "live.status": "状态",
    "live.qps": "QPS",
    "live.activeVus": "活跃 VU",
    "live.errorRate": "错误率",
    "live.live": "● 实时",
    "live.closed": "○ 已断开",
    "tests.title": "测试用例",
    "tests.new": "新建用例",
    "tests.name": "名称",
    "tests.protocol": "协议",
    "tests.plan": "压测计划 (JSON)",
    "tests.ramp": "压力曲线 (JSON 数组)",
    "tests.script": "脚本(可选,goja JS — 定义 iteration())",
    "tests.create": "创建用例",
    "tests.created": "用例已创建",
    "tests.jsonErr": "plan / ramp 必须是合法 JSON",
    "tests.colName": "名称",
    "tests.colProtocol": "协议",
    "tests.colCreated": "创建时间",
    "tests.empty": "尚未定义任何用例。",
    "workers.title": "工作节点",
    "workers.colWorker": "节点",
    "workers.colRegion": "区域",
    "workers.colStatus": "状态",
    "workers.colActive": "活跃 VU",
    "workers.colLastSeen": "最近心跳",
    "workers.empty": "暂无已连接的工作节点。",
    "users.title": "用户",
    "users.new": "新建用户",
    "users.email": "邮箱",
    "users.name": "姓名",
    "users.role": "角色",
    "users.password": "密码",
    "users.create": "创建",
    "users.adminRequired": "需要管理员权限。",
    "users.colEmail": "邮箱",
    "users.colName": "姓名",
    "users.colRole": "角色",
  },
  en: {
    "nav.runs": "Runs",
    "nav.tests": "Tests",
    "nav.workers": "Workers",
    "nav.users": "Users",
    "nav.signout": "Sign out",
    "login.title": "Sign in to loadify",
    "login.email": "Email",
    "login.password": "Password",
    "login.signin": "Sign in",
    "login.signingin": "Signing in…",
    "login.feishu": "Sign in with Feishu",
    "login.failed": "login failed",
    "login.feishuFailed": "feishu login failed",
    "runs.title": "Runs",
    "runs.start": "Start a run",
    "runs.test": "Test",
    "runs.selectTest": "Select a test…",
    "runs.workers": "Workers",
    "runs.startBtn": "Start run",
    "runs.colRun": "Run",
    "runs.colStatus": "Status",
    "runs.colWorkers": "Workers",
    "runs.colStarted": "Started",
    "runs.empty": "No runs yet.",
    "run.title": "Run",
    "run.stop": "Stop run",
    "run.throughput": "Throughput (QPS)",
    "run.latency": "Latency (ms)",
    "run.errorRate": "Error rate (%)",
    "run.summary": "Summary",
    "live.status": "Status",
    "live.qps": "QPS",
    "live.activeVus": "Active VUs",
    "live.errorRate": "Error rate",
    "live.live": "● live",
    "live.closed": "○ closed",
    "tests.title": "Tests",
    "tests.new": "New test",
    "tests.name": "Name",
    "tests.protocol": "Protocol",
    "tests.plan": "Plan (JSON)",
    "tests.ramp": "Ramp (JSON array of stages)",
    "tests.script": "Script (optional, goja JS — define iteration())",
    "tests.create": "Create test",
    "tests.created": "Test created",
    "tests.jsonErr": "plan/ramp must be valid JSON",
    "tests.colName": "Name",
    "tests.colProtocol": "Protocol",
    "tests.colCreated": "Created",
    "tests.empty": "No tests defined.",
    "workers.title": "Workers",
    "workers.colWorker": "Worker",
    "workers.colRegion": "Region",
    "workers.colStatus": "Status",
    "workers.colActive": "Active VUs",
    "workers.colLastSeen": "Last seen",
    "workers.empty": "No workers connected.",
    "users.title": "Users",
    "users.new": "New user",
    "users.email": "Email",
    "users.name": "Name",
    "users.role": "Role",
    "users.password": "Password",
    "users.create": "Create",
    "users.adminRequired": "Admin access required.",
    "users.colEmail": "Email",
    "users.colName": "Name",
    "users.colRole": "Role",
  },
};

const LANG_KEY = "loadify_lang";

interface I18n {
  lang: Lang;
  setLang: (l: Lang) => void;
  t: (key: string) => string;
}

const I18nContext = createContext<I18n>({
  lang: "zh",
  setLang: () => {},
  t: (k) => k,
});

export function LocaleProvider({ children }: { children: React.ReactNode }) {
  // Default to Chinese; both server and first client render use "zh" so there
  // is no hydration mismatch, then the stored preference is applied on mount.
  const [lang, setLangState] = useState<Lang>("zh");

  useEffect(() => {
    const stored = window.localStorage.getItem(LANG_KEY) as Lang | null;
    if (stored === "zh" || stored === "en") setLangState(stored);
  }, []);

  const setLang = (l: Lang) => {
    setLangState(l);
    window.localStorage.setItem(LANG_KEY, l);
    document.documentElement.lang = l === "zh" ? "zh-CN" : "en";
  };

  const t = (key: string) => messages[lang][key] ?? messages.en[key] ?? key;

  return <I18nContext.Provider value={{ lang, setLang, t }}>{children}</I18nContext.Provider>;
}

export function useI18n() {
  return useContext(I18nContext);
}
