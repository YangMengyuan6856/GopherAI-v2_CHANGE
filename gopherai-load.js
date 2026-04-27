import http from 'k6/http';
import { check, sleep } from 'k6';
import { Counter, Rate, Trend } from 'k6/metrics';

export const options = {
  scenarios: {
    conversation_flow: {
      executor: 'ramping-vus',
      exec: 'conversationFlow',
      startVUs: 0,
      stages: [
        { duration: '30s', target: 10 },
        { duration: '30s', target: 20 },
        { duration: '30s', target: 35 },
        { duration: '30s', target: 50 },
        { duration: '30s', target: 0 },
      ],
      gracefulRampDown: '30s',
    },
    session_reads: {
      executor: 'constant-vus',
      exec: 'sessionReadFlow',
      vus: 15,
      duration: '2m',
      startTime: '10s',
      gracefulStop: '15s',
    },
    login_burst: {
      executor: 'constant-arrival-rate',
      exec: 'loginBurstFlow',
      rate: 4,
      timeUnit: '1s',
      duration: '45s',
      preAllocatedVUs: 8,
      maxVUs: 20,
      startTime: '20s',
      gracefulStop: '10s',
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.05'],
    http_req_duration: ['p(95)<8000'],
    business_success_rate: ['rate>0.95'],
    login_duration: ['p(95)<1500'],
    create_session_duration: ['p(95)<8000'],
    continue_chat_duration: ['p(95)<8000'],
    history_duration: ['p(95)<3000'],
    session_list_duration: ['p(95)<2000'],
  },
};

const BASE_URL = 'http://101.200.145.78:8080/api';
const USERNAME = '70778586792';
const PASSWORD = '1234abc.';
const MODEL_TYPE = '1';
const SUCCESS_CODE = 1000;
const THINK_TIME_SECONDS = 0.2;
const QUESTIONS = [
  '请用三句话介绍人工智能。',
  '请解释一下大语言模型的基本原理。',
  '请说说 RAG 和普通对话模型的区别。',
  '请总结一下 AI 助手在开发中的常见应用。',
  '请举例说明智能问答系统的典型场景。',
];

const businessSuccessRate = new Rate('business_success_rate');
const loginDuration = new Trend('login_duration');
const createSessionDuration = new Trend('create_session_duration');
const continueChatDuration = new Trend('continue_chat_duration');
const historyDuration = new Trend('history_duration');
const sessionListDuration = new Trend('session_list_duration');
const createdSessions = new Counter('created_sessions');
const loginRequests = new Counter('login_requests');

function authHeaders(token) {
  return {
    'Content-Type': 'application/json',
    Authorization: `Bearer ${token}`,
  };
}

function safeJson(res) {
  try {
    return res.json();
  } catch (e) {
    return null;
  }
}

function isBusinessSuccess(body) {
  return body && body.status_code === SUCCESS_CODE;
}

function randomQuestion() {
  return QUESTIONS[Math.floor(Math.random() * QUESTIONS.length)];
}

function loginAndGetToken() {
  const loginRes = http.post(
    `${BASE_URL}/user/login`,
    JSON.stringify({
      username: USERNAME,
      password: PASSWORD,
    }),
    {
      headers: { 'Content-Type': 'application/json' },
      timeout: '30s',
      tags: { api_name: 'login' },
    }
  );

  loginRequests.add(1);
  loginDuration.add(loginRes.timings.duration);
  const body = safeJson(loginRes);
  const ok = check(loginRes, {
    'login http 200': (r) => r.status === 200,
    'login business success': () => isBusinessSuccess(body),
    'login has token': () => !!(body && body.token),
  });

  businessSuccessRate.add(ok);
  if (!ok) {
    throw new Error(`Login failed. Response: ${loginRes.body}`);
  }

  return body.token;
}

function createSession(token) {
  const res = http.post(
    `${BASE_URL}/AI/chat/send-new-session`,
    JSON.stringify({
      question: randomQuestion(),
      modelType: MODEL_TYPE,
    }),
    {
      headers: authHeaders(token),
      timeout: '120s',
      tags: { api_name: 'create_session' },
    }
  );

  createSessionDuration.add(res.timings.duration);
  const body = safeJson(res);
  const ok = check(res, {
    'create session http 200': (r) => r.status === 200,
    'create session business success': () => isBusinessSuccess(body),
    'create session has sessionId': () => !!(body && body.sessionId),
    'create session has answer': () => !!(body && body.Information),
  });

  businessSuccessRate.add(ok);
  if (!ok) {
    throw new Error(`Create session failed. Response: ${res.body}`);
  }

  createdSessions.add(1);
  return body;
}

function continueChat(token, sessionId) {
  const res = http.post(
    `${BASE_URL}/AI/chat/send`,
    JSON.stringify({
      question: '继续补充一句关于它在实际开发中的用途。',
      modelType: MODEL_TYPE,
      sessionId: sessionId,
    }),
    {
      headers: authHeaders(token),
      timeout: '120s',
      tags: { api_name: 'continue_chat' },
    }
  );

  continueChatDuration.add(res.timings.duration);
  const body = safeJson(res);
  const ok = check(res, {
    'continue chat http 200': (r) => r.status === 200,
    'continue chat business success': () => isBusinessSuccess(body),
    'continue chat has answer': () => !!(body && body.Information),
  });

  businessSuccessRate.add(ok);
  if (!ok) {
    throw new Error(`Continue chat failed. Response: ${res.body}`);
  }
}

function queryHistory(token, sessionId) {
  const res = http.post(
    `${BASE_URL}/AI/chat/history`,
    JSON.stringify({
      sessionId: sessionId,
    }),
    {
      headers: authHeaders(token),
      timeout: '60s',
      tags: { api_name: 'history' },
    }
  );

  historyDuration.add(res.timings.duration);
  const body = safeJson(res);
  const ok = check(res, {
    'history http 200': (r) => r.status === 200,
    'history business success': () => isBusinessSuccess(body),
    'history has records': () => Array.isArray(body && body.history),
  });

  businessSuccessRate.add(ok);
  if (!ok) {
    throw new Error(`History query failed. Response: ${res.body}`);
  }
}

function listSessions(token) {
  const res = http.get(`${BASE_URL}/AI/chat/sessions`, {
    headers: {
      Authorization: `Bearer ${token}`,
    },
    timeout: '30s',
    tags: { api_name: 'session_list' },
  });

  sessionListDuration.add(res.timings.duration);
  const body = safeJson(res);
  const ok = check(res, {
    'session list http 200': (r) => r.status === 200,
    'session list business success': () => isBusinessSuccess(body),
    'session list has array': () => Array.isArray(body && body.sessions),
  });

  businessSuccessRate.add(ok);
  if (!ok) {
    throw new Error(`Session list failed. Response: ${res.body}`);
  }
}

export function setup() {
  const token = loginAndGetToken();
  return { token };
}

export function conversationFlow(data) {
  const created = createSession(data.token);
  continueChat(data.token, created.sessionId);
  queryHistory(data.token, created.sessionId);
  sleep(THINK_TIME_SECONDS);
}

export function sessionReadFlow(data) {
  listSessions(data.token);
  sleep(0.5);
}

export function loginBurstFlow() {
  const token = loginAndGetToken();
  if (token) {
    sleep(0.1);
  }
}
