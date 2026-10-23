const AI_PROVIDER_TYPES = [
  { type: "claude", provider: "claude", list: "claude-api-key", brand: "claude" },
  { type: "codex", provider: "codex", list: "codex-api-key", brand: "codex" },
  { type: "gemini", provider: "gemini", list: "gemini-api-key", brand: "gemini" },
  { type: "interactions", provider: "gemini-interactions", list: "interactions-api-key", brand: "gemini" },
  { type: "vertex", provider: "vertex", list: "vertex-api-key", brand: "vertex" },
  { type: "xai", provider: "xai", list: "xai-api-key", brand: "xai" },
  { type: "meta", provider: "meta", list: "meta-api-key", brand: "meta" },
  { type: "openai-compatibility", list: "openai-compatibility", brand: "openai" }
];

function sortedConfiguredHeaders(headers) {
  if (!headers) return "";
  return Object.keys(headers).sort().map((key) => key + "\0" + headers[key] + "\0").join("");
}

async function configuredCredentialRef(kind, parts, counters) {
  // These hashes must stay byte-identical to CPA's StableIDGenerator and
  // the plugin's CredentialFingerprint so exact bindings match candidates.
  const digest = await sha256Hex(kind + parts.map((part) => "\0" + String(part || "").trim()).join(""));
  const base = kind + ":" + digest.slice(0, 12);
  const collision = counters.get(base) || 0;
  counters.set(base, collision + 1);
  const id = collision ? base + "-" + collision : base;
  return "sha256:" + (await sha256Hex("cpa-key-billing:credential:v1\0" + id));
}

function configuredProviderDetails(entry, type) {
  return {
    type,
    name: (entry.name || "").trim(),
    auth_index: entry["auth-index"] || "",
    base_url: (entry["base-url"] || "").trim(),
    priority: entry.priority ?? 0
  };
}

async function syncConfiguredProviders() {
  const config = Object.assign(
    {},
    ...(await Promise.all(AI_PROVIDER_TYPES.map((kind) => api("GET", "/v0/management/" + kind.list, null))))
  );
  return plugin("POST", "/credentials/sync", { credentials: await parseConfiguredCredentials(config) }, { command: true });
}

async function parseConfiguredCredentials(config) {
  const generation = sessionGeneration;
  const counters = new Map();
  const disabledCounters = new Map();
  const credentials = [];

  for (const { type, list, provider } of AI_PROVIDER_TYPES) {
    if (type === "openai-compatibility") continue;
    for (const entry of config[list] || []) {
      const apiKey = (entry["api-key"] || "").trim();
      const baseURL = (entry["base-url"] || "").trim();
      if (!apiKey && !baseURL) continue;
      const parts = [apiKey, baseURL, entry["proxy-url"]];
      if (type !== "vertex") parts.push(entry.prefix, sortedConfiguredHeaders(entry.headers));
      credentials.push({
        ref: await configuredCredentialRef(provider + ":apikey", parts, counters),
        ...configuredProviderDetails(entry, type),
        provider,
        key_preview: previewCredential(apiKey),
        weight: Math.max(0, entry.weight ?? 1),
        disabled: (entry["excluded-models"] || []).some((model) => model.trim() === "*")
      });
    }
  }

  for (const entry of config["openai-compatibility"] || []) {
    // Disabled providers have no runtime identity and must not consume the live ID counters.
    const disabled = entry.disabled === true;
    const kindPrefix = disabled ? "disabled:" : "";
    const providerCounters = disabled ? disabledCounters : counters;
    const providerName = (entry.name || "").trim().toLowerCase() || "openai-compatibility";
    const provider =
      providerName === "openai-compatibility" || providerName.startsWith("openai-compatible-")
        ? providerName
        : "openai-compatible-" + providerName;
    const apiKeys = entry["api-key-entries"] || [];
    if (!apiKeys.length) {
      credentials.push({
        ref: await configuredCredentialRef(kindPrefix + "openai-compatibility:" + providerName, [entry["base-url"]], providerCounters),
        ...configuredProviderDetails(entry, "openai-compatibility"),
        provider,
        key_preview: "",
        weight: 1,
        disabled
      });
      continue;
    }
    for (const apiKeyEntry of apiKeys) {
      const apiKey = (apiKeyEntry["api-key"] || "").trim();
      credentials.push({
        ref: await configuredCredentialRef(
          kindPrefix + "openai-compatibility:" + providerName,
          [apiKey, entry["base-url"], apiKeyEntry["proxy-url"]],
          providerCounters
        ),
        ...configuredProviderDetails(entry, "openai-compatibility"),
        auth_index: apiKeyEntry["auth-index"] || "",
        provider,
        key_preview: previewCredential(apiKey),
        weight: Math.max(0, apiKeyEntry.weight ?? 1),
        disabled
      });
    }
  }

  if (generation !== sessionGeneration) throw new StaleRequestError();
  return credentials;
}

async function fetchAIProviders(account) {
  if (!account) await queueAdminData("configuration", syncConfiguredProviders);
  const range = selectedRange(account);
  const path = "/ai-providers?" + new URLSearchParams(range);
  const result = account ? await accountAPI(path) : await plugin("GET", path);
  return result.providers.map((provider) => ({
    ...provider,
    usage_query: provider.auth_index ? { credential_ref: provider.ref, ...range } : null
  }));
}

function readAIProviders(account, reload = false) {
  const role = account ? "account" : "admin";
  return aiProvidersQueries[role].read(JSON.stringify(sharedTimeRanges[role]), () => fetchAIProviders(account), reload);
}

function filteredAIProviders(account) {
  const prefix = account ? "account-" : "";
  const type = $(prefix + "ai-provider-type").value;
  const search = $(prefix + "ai-provider-search").value.trim().toLocaleLowerCase();
  return (aiProvidersQueries[account ? "account" : "admin"].value || []).filter((provider) => {
    if ($(prefix + "ai-provider-enabled-only").checked && provider.disabled) return false;
    if (type && provider.type !== type) return false;
    if (!search) return true;
    return [provider.base_url, provider.type, provider.name, provider.key_preview].some((value) =>
      value.toLocaleLowerCase().includes(search)
    );
  });
}

function renderAIProviders(account) {
  const body = $((account ? "account-" : "") + "ai-providers-body");
  const bulk = account ? null : $("ai-provider-query-balances");
  if (renderDataPending(body, [aiProvidersQueries[account ? "account" : "admin"]])) {
    if (bulk) bulk.disabled = true;
    return;
  }
  const providers = filteredAIProviders(account);
  if (bulk) bulk.disabled = bulk.dataset.loading === "true" || !providers.some(aiBalanceUpstream);
  renderCollection(body, providers, {
    headers: [
      m("ui.ai_providers"),
      ...credentialMetricHeaders(),
      { label: m("ui.balance"), class: "credential-quota-column" },
      ...(!account ? [m("ui.actions")] : [])
    ],
    attrs: { class: "credential-table" },
    empty: m("ui.no_ai_providers_match_the_filters"),
    render: (provider) => renderAIProviderRow(provider, account)
  });
}

function renderAIProviderRow(provider, account) {
  const upstream = aiBalanceUpstream(provider);
  const loading = adminUIState.aiBalanceLoading.has(provider.auth_index);
  const baseURL = provider.base_url.replace(/^https:\/\//i, "");
  return el(
    "tr",
    { class: "credential-row" + (provider.disabled ? " disabled" : "") },
    el(
      "td",
      { class: "credential-identity-cell" },
      el(
        "div",
        { class: "credential-head" },
        providerIcon(AI_PROVIDER_TYPES.find((kind) => kind.type === provider.type)?.brand),
        el(
          "div",
          { class: "credential-identity" },
          el("div", {
            class: "credential-title" + (baseURL ? "" : " muted"),
            "data-tip": baseURL || null,
            "data-tip-overflow": "",
            text: baseURL || m("ui.default_base_url")
          }),
          el(
            "div",
            { class: "credential-meta ai-provider-meta" },
            el(
              "span",
              { class: "tag plain ai-provider-key" },
              el("span", {
                class: provider.key_preview ? "mono mask-blur" : "",
                text: provider.key_preview || m("ui.no_api_key_configured")
              })
            ),
            credentialRoutingTag(provider),
            provider.disabled && el("span", { class: "tag warn", text: m("ui.disabled") })
          )
        )
      )
    ),
    renderCredentialMetrics(provider, modelUsageTooltip(account, provider.usage_query)),
    el("td", { class: "credential-quota-cell" }, renderAIBalance(provider, account)),
    !account &&
      el(
        "td",
        { class: "credential-actions-cell" + (upstream ? "" : " unavailable") },
        el(
          "div",
          { class: "credential-actions" },
          el(
            "button",
            {
              type: "button",
              class: "labeled-icon-button round-icon-button" + (loading ? " loading" : ""),
              disabled: !upstream || loading || $("ai-provider-query-balances").dataset.loading === "true",
              "aria-label": m("ui.query_balance"),
              "data-tip": upstream ? m("ui.query_balance") : m("ui.balance_queries_are_not_supported_for_this_provider"),
              onclick: () => guard(() => queryAIBalance(provider))
            },
            actionIcon("refresh")
          )
        )
      )
  );
}

for (const account of [false, true]) {
  const prefix = account ? "account-" : "";
  $(prefix + "ai-provider-type").append(...AI_PROVIDER_TYPES.map((kind) => el("option", { value: kind.type, text: kind.type })));
  for (const filter of [$(prefix + "ai-provider-enabled-only"), $(prefix + "ai-provider-type")]) {
    filter.addEventListener("change", () => {
      saveChoice(account ? "account" : "admin", filter.id);
      renderAIProviders(account);
    });
  }
  $(prefix + "ai-provider-search").addEventListener("input", () => renderAIProviders(account));
}
resetAIBalanceBatch();
$("ai-provider-query-balances").onclick = () => guard(queryFilteredAIBalances);
