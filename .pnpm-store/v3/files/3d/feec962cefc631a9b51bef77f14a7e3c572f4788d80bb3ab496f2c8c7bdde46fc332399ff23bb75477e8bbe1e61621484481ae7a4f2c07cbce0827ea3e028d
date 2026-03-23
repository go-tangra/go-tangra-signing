import { createRequire } from "node:module";
import defu from "defu";
import * as fs from "fs";
import { existsSync, mkdirSync, readFileSync, writeFile, writeFileSync } from "fs";
import { createRequire as createRequire$1 } from "module";
import * as path$1 from "pathe";
import path, { basename, dirname, join, parse, resolve } from "pathe";
import MagicString from "magic-string";
import { createFilter } from "@rollup/pluginutils";
import { normalizeOptions } from "@module-federation/sdk";
import { consumeTypesAPI, generateTypesAPI, isTSProject, normalizeConsumeTypesOptions, normalizeDtsOptions, normalizeGenerateTypesOptions } from "@module-federation/dts-plugin";
import { rpc } from "@module-federation/dts-plugin/core";
import { fileURLToPath } from "url";
//#region \0rolldown/runtime.js
var __require = /* @__PURE__ */ createRequire(import.meta.url);
//#endregion
//#region src/utils/mapCodeToCodeWithSourcemap.ts
async function mapCodeToCodeWithSourcemap(code) {
	const resolvedCode = await code;
	if (resolvedCode === void 0) return;
	const s = new MagicString(resolvedCode);
	return {
		code: s.toString(),
		map: s.generateMap({ hires: true })
	};
}
//#endregion
//#region src/utils/htmlEntryUtils.ts
function sanitizeDevEntryPath(devEntryPath) {
	return devEntryPath.replace(/^[^:]+:([/\\])[/\\]?/, "$1").replace(/\\\\?/g, "/");
}
/**
* Inlines the federation init import into existing module script tags to fix
* the race condition (#396) where separate `<script type="module">` tags
* don't guarantee execution order with top-level await.
*
* If no entry scripts are found, falls back to injecting a separate script tag.
*
* @example
* // Before (two separate scripts, race condition):
* //   <script type="module" src="/__mf__virtual/hostAutoInit.js"><\/script>
* //   <script type="module" src="/src/main.js"><\/script>
* // After (single inline script, sequential execution):
* //   <script type="module">await import("/__mf__virtual/hostAutoInit.js");await import("/src/main.js");<\/script>
*/
function inlineEntryScripts(html, initSrc) {
	const src = sanitizeDevEntryPath(initSrc);
	const scriptTagRegex = /<script\s+([^>]*\btype=["']module["'][^>]*\bsrc=["'][^"']+["'][^>]*)>/gi;
	let hasEntry = false;
	const result = html.replace(scriptTagRegex, (match, attrs) => {
		const srcMatch = attrs.match(/\bsrc=["']([^"']+)["']/i);
		if (!srcMatch) return match;
		const originalSrc = srcMatch[1];
		if (originalSrc.includes("@vite/client")) return match;
		hasEntry = true;
		return `<script ${attrs.replace(/\s*\bsrc=["'][^"']+["']/i, "")}>await import(${JSON.stringify(src)});await import(${JSON.stringify(originalSrc)});`;
	});
	if (hasEntry) return result;
	return html.replace("<head>", `<head><script type="module" src=${JSON.stringify(src)}><\/script>`);
}
//#endregion
//#region src/utils/packageUtils.ts
const dependencyPresenceCache = /* @__PURE__ */ new Map();
let packageDetectionCwd;
function getDependencyCacheKey(cwd, dependencyName) {
	return `${cwd}:${dependencyName}`;
}
function setPackageDetectionCwd(cwd) {
	packageDetectionCwd = cwd;
}
/**
* Escaping rules:
* Convert using the format __${mapping}__, where _ and $ are not allowed in npm package names but can be used in variable names.
*  @ => 1
*  / => 2
*  - => 3
*  . => 4
*/
/**
* Encodes a package name into a valid file name.
* @param {string} name - The package name, e.g., "@scope/xx-xx.xx".
* @returns {string} - The encoded file name.
*/
function packageNameEncode(name) {
	if (typeof name !== "string") throw new Error("A string package name is required");
	return name.replace(/@/g, "_mf_0_").replace(/\//g, "_mf_1_").replace(/-/g, "_mf_2_").replace(/\./g, "_mf_3_");
}
/**
* Decodes an encoded file name back to the original package name.
* @param {string} encoded - The encoded file name, e.g., "_mf_0_scope_mf_1_xx_mf_2_xx_mf_3_xx".
* @returns {string} - The decoded package name.
*/
function packageNameDecode(encoded) {
	if (typeof encoded !== "string") throw new Error("A string encoded file name is required");
	return encoded.replace(/_mf_0_/g, "@").replace(/_mf_1_/g, "/").replace(/_mf_2_/g, "-").replace(/_mf_3_/g, ".");
}
/**
* Removes any subpath from an npm package specifier and returns the package name only.
* @param {string} packageString - The package specifier, e.g., "@scope/pkg/runtime" or "react/jsx-runtime".
* @returns {string} - The base npm package name.
*/
function removePathFromNpmPackage(packageString) {
	const match = packageString.match(/^(?:@[^/]+\/)?[^/]+/);
	return match ? match[0] : packageString;
}
function hasPackageDependency(dependencyName, cwd = packageDetectionCwd || process.cwd()) {
	const cacheKey = getDependencyCacheKey(cwd, dependencyName);
	const cached = dependencyPresenceCache.get(cacheKey);
	if (cached !== void 0) return cached;
	try {
		const packageJson = JSON.parse(readFileSync(path.join(cwd, "package.json"), "utf8"));
		const hasDependency = [
			packageJson.dependencies,
			packageJson.devDependencies,
			packageJson.peerDependencies,
			packageJson.optionalDependencies
		].some((deps) => !!deps?.[dependencyName]);
		dependencyPresenceCache.set(cacheKey, hasDependency);
		return hasDependency;
	} catch {
		dependencyPresenceCache.set(cacheKey, false);
		return false;
	}
}
//#endregion
//#region src/plugins/pluginAddEntry.ts
function getFirstHtmlEntryFile(entryFiles) {
	return entryFiles.find((file) => file.endsWith(".html"));
}
const addEntry = ({ entryName, entryPath, fileName, inject = "entry" }) => {
	const getEntryPath = () => typeof entryPath === "function" ? entryPath() : entryPath;
	let devEntryPath = "";
	let entryFiles = [];
	let htmlFilePath;
	let _command;
	let emitFileId;
	let viteConfig;
	let clientInjected = false;
	function injectHtml() {
		return inject === "html" && htmlFilePath;
	}
	function injectEntry() {
		return inject === "entry" || !htmlFilePath;
	}
	return [{
		name: "add-entry",
		apply: "serve",
		config(config, { command }) {
			_command = command;
		},
		configResolved(config) {
			viteConfig = config;
			const resolvedEntryPath = getEntryPath();
			devEntryPath = resolvedEntryPath.startsWith("virtual:mf") ? "@id/" + resolvedEntryPath : resolvedEntryPath;
			devEntryPath = config.base + devEntryPath.replace(/\\\\?/g, "/").replace(/^[^:]+:([/\\])[/\\]?/, "$1").replace(/^\//, "");
		},
		configureServer(server) {
			server.middlewares.use((req, res, next) => {
				if (!fileName) {
					next();
					return;
				}
				if (req.url && req.url.startsWith((viteConfig.base + fileName).replace(/^\/?/, "/"))) req.url = devEntryPath;
				next();
			});
		},
		transformIndexHtml(c) {
			if (!injectHtml()) return;
			clientInjected = true;
			return inlineEntryScripts(c, devEntryPath);
		},
		transform(code, id) {
			if (id.includes("node_modules") || inject !== "html" || htmlFilePath) return;
			if (id.includes(".svelte-kit") && id.includes("internal.js")) return code.replace(/<head>/g, "<head><script type=\\\"module\\\" src=\\\"" + sanitizeDevEntryPath(devEntryPath) + "\\\"><\/script>");
		}
	}, {
		name: "add-entry",
		enforce: "post",
		configResolved(config) {
			viteConfig = config;
			const inputOptions = config.build.rollupOptions.input;
			if (!inputOptions) htmlFilePath = path$1.resolve(config.root, "index.html");
			else if (typeof inputOptions === "string") entryFiles = [inputOptions];
			else if (Array.isArray(inputOptions)) entryFiles = inputOptions;
			else if (typeof inputOptions === "object") entryFiles = Object.values(inputOptions);
			if (entryFiles && entryFiles.length > 0) htmlFilePath = getFirstHtmlEntryFile(entryFiles);
			if (_command === "serve" && htmlFilePath && fs.existsSync(htmlFilePath)) {
				const htmlContent = fs.readFileSync(htmlFilePath, "utf-8");
				const scriptRegex = /<script\s+[^>]*src=["']([^"']+)["'][^>]*>/gi;
				let match;
				while ((match = scriptRegex.exec(htmlContent)) !== null) entryFiles.push(match[1]);
			}
		},
		buildStart() {
			if (_command === "serve") return;
			const hasHash = fileName?.includes?.("[hash");
			const emitFileOptions = {
				name: entryName,
				type: "chunk",
				id: getEntryPath(),
				preserveSignature: "strict"
			};
			if (!hasHash) emitFileOptions.fileName = fileName;
			emitFileId = this.emitFile(emitFileOptions);
			if (htmlFilePath && fs.existsSync(htmlFilePath)) {
				const htmlContent = fs.readFileSync(htmlFilePath, "utf-8");
				const scriptRegex = /<script\s+[^>]*src=["']([^"']+)["'][^>]*>/gi;
				let match;
				while ((match = scriptRegex.exec(htmlContent)) !== null) entryFiles.push(match[1]);
			}
		},
		generateBundle(options, bundle) {
			if (!injectHtml()) return;
			const file = this.getFileName(emitFileId);
			const resolvePath = (htmlFileName) => {
				if (!viteConfig.experimental?.renderBuiltUrl) return viteConfig.base + file;
				const result = viteConfig.experimental.renderBuiltUrl(file, {
					hostId: htmlFileName,
					hostType: "html",
					type: "asset",
					ssr: false
				});
				if (typeof result === "string") return result;
				if (result && typeof result === "object") {
					if ("runtime" in result) {
						console.warn("[vite-plugin-federation] renderBuiltUrl returned runtime code for HTML injection. Runtime code cannot be used in <script src=\"\">. Falling back to base path.");
						return viteConfig.base + file;
					}
					if (result.relative) return file;
				}
				return viteConfig.base + file;
			};
			for (const fileName in bundle) if (fileName.endsWith(".html")) {
				let htmlAsset = bundle[fileName];
				if (htmlAsset.type === "chunk") return;
				const scriptContent = `
          <script type="module" src="${resolvePath(fileName)}"><\/script>
        `;
				let htmlContent = htmlAsset.source.toString() || "";
				htmlContent = htmlContent.replace("<head>", `<head>${scriptContent}`);
				htmlAsset.source = htmlContent;
			}
		},
		transform(code, id) {
			if (hasPackageDependency("vinext") && inject === "html" && (id.includes("virtual:vite-rsc/entry-browser") || id.includes("virtual:vinext-app-browser-entry"))) {
				const injection = `import ${JSON.stringify(getEntryPath())};\n`;
				if (code.includes(injection.trim())) {
					clientInjected = true;
					return;
				}
				clientInjected = true;
				return mapCodeToCodeWithSourcemap(injection + code);
			}
			if (injectEntry() && entryFiles.some((file) => id.endsWith(file)) || _command === "serve" && inject === "html" && !clientInjected && !id.includes("node_modules") && /\.(js|ts|mjs|vue|jsx|tsx)(\?|$)/.test(id)) {
				clientInjected = true;
				return mapCodeToCodeWithSourcemap(`import ${JSON.stringify(getEntryPath())};\n` + code);
			}
		}
	}];
};
//#endregion
//#region src/plugins/pluginCheckAliasConflicts.ts
/**
* Check if user-defined alias conflicts with shared modules
* This should run after aliasToArrayPlugin to ensure alias is an array
*/
function checkAliasConflicts(options) {
	const { shared = {} } = options;
	const sharedKeys = Object.keys(shared);
	return {
		name: "check-alias-conflicts",
		configResolved(config) {
			if (sharedKeys.length === 0) return;
			const userAliases = config.resolve?.alias || [];
			const conflicts = [];
			for (const sharedKey of sharedKeys) for (const aliasEntry of userAliases) {
				const findPattern = aliasEntry.find;
				const replacement = aliasEntry.replacement;
				if (typeof replacement !== "string") continue;
				if (replacement === "$1") continue;
				let isMatch = false;
				if (typeof findPattern === "string") isMatch = findPattern === sharedKey || sharedKey.startsWith(findPattern + "/");
				else if (findPattern instanceof RegExp) isMatch = findPattern.test(sharedKey);
				if (isMatch) conflicts.push({
					sharedModule: sharedKey,
					alias: String(findPattern),
					target: replacement
				});
			}
			if (conflicts.length > 0) {
				config.logger.warn("\n[Module Federation] Detected alias conflicts with shared modules:");
				conflicts.forEach(({ sharedModule, alias, target }) => {
					config.logger.warn(`  - Shared module "${sharedModule}" is aliased by "${alias}" to "${target}"`);
				});
				config.logger.warn("  This may cause runtime errors as the shared module will bypass Module Federation's sharing mechanism.");
			}
		}
	};
}
//#endregion
//#region src/plugins/pluginDevProxyModuleTopLevelAwait.ts
/**
* Solve the problem that dev mode dependency prebunding does not support top-level await syntax
*/
let walkPromise = null;
function loadWalk() {
	walkPromise ||= import("estree-walker").then(({ walk }) => walk);
	return walkPromise;
}
function PluginDevProxyModuleTopLevelAwait() {
	const filterFunction = createFilter();
	const processedFlag = "/* already-processed-by-dev-proxy-module-top-level-await */";
	return {
		name: "dev-proxy-module-top-level-await",
		apply: "serve",
		async transform(code, id) {
			if (code.includes(processedFlag)) return null;
			if (!code.includes("/*mf top-level-await placeholder replacement mf*/")) return null;
			if (!filterFunction(id)) return null;
			let ast;
			try {
				ast = this.parse(code, { allowReturnOutsideFunction: true });
			} catch (e) {
				throw new Error(`${id}: ${e}`);
			}
			const magicString = new MagicString(code);
			const walk = await loadWalk();
			const defaultExportExpression = hasPackageDependency("vinext") ? "(__mfproxy__awaitdefault?.default ?? __mfproxy__awaitdefault)" : "__mfproxy__awaitdefault";
			walk(ast, { enter(node) {
				if (node.type === "ExportNamedDeclaration" && node.specifiers) {
					const exportSpecifiers = node.specifiers.map((specifier) => specifier.exported.name);
					const proxyStatements = exportSpecifiers.map((name) => `
              const __mfproxy__await${name} = await ${name}();
              const __mfproxy__${name} = () => __mfproxy__await${name};
            `).join("\n");
					const exportStatements = exportSpecifiers.map((name) => `__mfproxy__${name} as ${name}`).join(", ");
					const start = node.start;
					const end = node.end;
					const replacement = `${proxyStatements}\nexport { ${exportStatements} };`;
					magicString.overwrite(start, end, replacement);
				}
				if (node.type === "ExportDefaultDeclaration") {
					const declaration = node.declaration;
					const start = node.start;
					const end = node.end;
					let proxyStatement;
					let exportStatement = "default";
					if (declaration.type === "Identifier") proxyStatement = `
                const __mfproxy__awaitdefault = await ${declaration.name}();
                const __mfproxy__default = ${defaultExportExpression};
              `;
					else if (declaration.type === "CallExpression" || declaration.type === "FunctionDeclaration") proxyStatement = `
                const __mfproxy__awaitdefault = await (${code.slice(declaration.start, declaration.end)});
                const __mfproxy__default = ${defaultExportExpression};
              `;
					else proxyStatement = `
                const __mfproxy__awaitdefault = await (${code.slice(declaration.start, declaration.end)});
                const __mfproxy__default = ${defaultExportExpression};
              `;
					const replacement = `${proxyStatement}\nexport { __mfproxy__default as ${exportStatement} };`;
					magicString.overwrite(start, end, replacement);
				}
			} });
			return {
				code: `${processedFlag}\n${magicString.toString()}`,
				map: magicString.generateMap({ hires: true })
			};
		}
	};
}
//#endregion
//#region src/plugins/pluginDts.ts
const DEFAULT_DEV_OPTIONS = {
	disableLiveReload: true,
	disableHotTypesReload: false,
	disableDynamicRemoteTypeHints: false
};
const DYNAMIC_HINTS_PLUGIN = "@module-federation/dts-plugin/dynamic-remote-type-hints-plugin";
const getIPv4 = () => process.env["FEDERATION_IPV4"] || "127.0.0.1";
const forkDevWorkerPath = __require.resolve("@module-federation/dts-plugin/dist/fork-dev-worker.js");
var DevWorker = class {
	constructor(options) {
		this.worker = rpc.createRpcWorker(forkDevWorkerPath, {}, void 0, false);
		this.worker.connect(options);
	}
	update() {
		this.worker.process?.send?.({
			type: rpc.RpcGMCallTypes.CALL,
			id: this.worker.id,
			args: [void 0, "update"]
		});
	}
	exit() {
		this.worker.terminate();
	}
};
const normalizeDevOptions = (dev) => {
	if (dev === false) return false;
	if (dev === true || typeof dev === "undefined") return { ...DEFAULT_DEV_OPTIONS };
	return {
		...DEFAULT_DEV_OPTIONS,
		...dev
	};
};
const buildDtsModuleFederationConfig = (options) => {
	const exposes = {};
	Object.entries(options.exposes).forEach(([key, value]) => {
		if (typeof value === "string") {
			exposes[key] = value;
			return;
		}
		const importValue = Array.isArray(value.import) ? value.import[0] : value.import;
		if (importValue) exposes[key] = importValue;
	});
	const remotes = {};
	Object.entries(options.remotes).forEach(([key, remote]) => {
		if (typeof remote === "string") {
			remotes[key] = remote;
			return;
		}
		if (!remote.entry) return;
		remotes[key] = `${remote.entryGlobalName?.startsWith("http") || remote.entryGlobalName?.includes(".json") ? remote.name || key : remote.entryGlobalName || remote.name || key}@${remote.entry}`;
	});
	return {
		...options,
		exposes,
		remotes
	};
};
const resolveOutputDir = (config) => {
	const { outDir } = config.build;
	if (path$1.isAbsolute(outDir)) return path$1.relative(config.root, outDir);
	return outDir;
};
const ensureRuntimePlugin = (options, pluginId) => {
	if (!options.runtimePlugins.some((plugin) => {
		if (typeof plugin === "string") return plugin === pluginId;
		return plugin[0] === pluginId;
	})) options.runtimePlugins.push(pluginId);
};
const normalizeDevDtsOptions = (dts, context) => {
	return normalizeOptions(isTSProject(dts, context), {
		generateTypes: { compileInChildProcess: true },
		consumeTypes: { consumeAPITypes: true },
		extraOptions: {},
		displayErrorInTerminal: typeof dts === "object" && dts ? dts.displayErrorInTerminal : void 0
	}, "mfOptions.dts")(dts);
};
const logDtsError = (error, dtsOptions) => {
	if (dtsOptions === false) return;
	if (typeof dtsOptions === "object" && dtsOptions && dtsOptions.displayErrorInTerminal === false) return;
	console.error(error);
};
function pluginDts(options) {
	if (options.dts === false) return [];
	const dtsModuleFederationConfig = buildDtsModuleFederationConfig(options);
	let resolvedConfig;
	let devWorker;
	let normalizedDevOptions;
	let hasGeneratedBundle = false;
	return [{
		name: "module-federation-dts-dev",
		apply: "serve",
		config(config) {
			normalizedDevOptions = normalizeDevOptions(options.dev);
			if (!normalizedDevOptions) return;
			if (normalizedDevOptions.disableDynamicRemoteTypeHints) return;
			ensureRuntimePlugin(options, DYNAMIC_HINTS_PLUGIN);
			const define = config.define ? { ...config.define } : {};
			if (!("FEDERATION_IPV4" in define)) define.FEDERATION_IPV4 = JSON.stringify(getIPv4());
			config.define = define;
		},
		configResolved(config) {
			resolvedConfig = config;
		},
		configureServer(server) {
			if (!normalizedDevOptions || !resolvedConfig) return;
			const devOptions = normalizedDevOptions;
			if (devOptions.disableDynamicRemoteTypeHints && devOptions.disableHotTypesReload && devOptions.disableLiveReload) return;
			if (!options.name) throw new Error("name is required if you want to enable dev server!");
			const outputDir = resolveOutputDir(resolvedConfig);
			const normalizedDtsOptions = normalizeDevDtsOptions(options.dts, resolvedConfig.root);
			if (typeof normalizedDtsOptions !== "object") return;
			const normalizedGenerateTypes = normalizeOptions(Boolean(normalizedDtsOptions), { compileInChildProcess: true }, "mfOptions.dts.generateTypes")(normalizedDtsOptions.generateTypes);
			const remote = normalizedGenerateTypes === false ? void 0 : {
				implementation: normalizedDtsOptions.implementation,
				context: resolvedConfig.root,
				outputDir,
				moduleFederationConfig: { ...dtsModuleFederationConfig },
				hostRemoteTypesFolder: normalizedGenerateTypes.typesFolder || "@mf-types",
				...normalizedGenerateTypes,
				typesFolder: ".dev-server"
			};
			if (remote && !remote.tsConfigPath && typeof normalizedDtsOptions === "object" && normalizedDtsOptions.tsConfigPath) remote.tsConfigPath = normalizedDtsOptions.tsConfigPath;
			const normalizedConsumeTypes = normalizeOptions(Boolean(normalizedDtsOptions), { consumeAPITypes: true }, "mfOptions.dts.consumeTypes")(normalizedDtsOptions.consumeTypes);
			const host = normalizedConsumeTypes === false ? void 0 : {
				implementation: normalizedDtsOptions.implementation,
				context: resolvedConfig.root,
				moduleFederationConfig: dtsModuleFederationConfig,
				typesFolder: normalizedConsumeTypes.typesFolder || "@mf-types",
				abortOnError: false,
				...normalizedConsumeTypes
			};
			const extraOptions = normalizedDtsOptions.extraOptions || {};
			if (!remote && !host && devOptions.disableLiveReload) return;
			const startDevWorker = async () => {
				let remoteTypeUrls;
				if (host) remoteTypeUrls = await new Promise((resolve) => {
					consumeTypesAPI({
						host,
						extraOptions,
						displayErrorInTerminal: normalizedDtsOptions.displayErrorInTerminal
					}, resolve);
				});
				devWorker = new DevWorker({
					name: options.name,
					remote,
					host: host ? {
						...host,
						remoteTypeUrls
					} : void 0,
					extraOptions,
					disableLiveReload: devOptions.disableLiveReload,
					disableHotTypesReload: devOptions.disableHotTypesReload
				});
				const update = () => devWorker?.update();
				server.watcher.on("change", update);
				server.watcher.on("add", update);
				server.watcher.on("unlink", update);
				server.httpServer?.once("close", () => {
					devWorker?.exit();
					server.watcher.off("change", update);
					server.watcher.off("add", update);
					server.watcher.off("unlink", update);
				});
			};
			startDevWorker().catch((error) => {
				logDtsError(error, normalizedDtsOptions);
			});
		}
	}, {
		name: "module-federation-dts-build",
		apply: "build",
		configResolved(config) {
			resolvedConfig = config;
		},
		async generateBundle() {
			if (hasGeneratedBundle) return;
			hasGeneratedBundle = true;
			if (!resolvedConfig) return;
			let normalizedDtsOptions;
			try {
				normalizedDtsOptions = normalizeDtsOptions(dtsModuleFederationConfig, resolvedConfig.root);
			} catch (error) {
				logDtsError(error, options.dts);
				return;
			}
			if (typeof normalizedDtsOptions !== "object") return;
			const context = resolvedConfig.root;
			const outputDir = resolveOutputDir(resolvedConfig);
			let consumeOptions;
			try {
				consumeOptions = normalizeConsumeTypesOptions({
					context,
					dtsOptions: normalizedDtsOptions,
					pluginOptions: dtsModuleFederationConfig
				});
			} catch (error) {
				logDtsError(error, normalizedDtsOptions);
				return;
			}
			if (consumeOptions?.host?.typesOnBuild) try {
				await consumeTypesAPI(consumeOptions);
			} catch (error) {
				logDtsError(error, normalizedDtsOptions);
			}
			let generateOptions;
			try {
				generateOptions = normalizeGenerateTypesOptions({
					context,
					outputDir,
					dtsOptions: normalizedDtsOptions,
					pluginOptions: dtsModuleFederationConfig
				});
			} catch (error) {
				logDtsError(error, normalizedDtsOptions);
				return;
			}
			if (!generateOptions) return;
			try {
				await generateTypesAPI({ dtsManagerOptions: generateOptions });
			} catch (error) {
				logDtsError(error, normalizedDtsOptions);
			}
		}
	}];
}
//#endregion
//#region src/utils/normalizeModuleFederationOptions.ts
function normalizeExposesItem(key, item) {
	let importPath = "";
	if (typeof item === "string") importPath = item;
	if (typeof item === "object") importPath = item.import;
	return { import: importPath };
}
function normalizeExposes(exposes) {
	if (!exposes) return {};
	const res = {};
	Object.keys(exposes).forEach((key) => {
		res[key] = normalizeExposesItem(key, exposes[key]);
	});
	return res;
}
function normalizeRemotes(remotes) {
	if (!remotes) return {};
	const result = {};
	if (typeof remotes === "object") Object.keys(remotes).forEach((key) => {
		result[key] = normalizeRemoteItem(key, remotes[key]);
	});
	return result;
}
function normalizeRemoteItem(key, remote) {
	if (typeof remote === "string") {
		const [entryGlobalName] = remote.split("@");
		return {
			type: "var",
			name: key,
			entry: remote.replace(entryGlobalName + "@", ""),
			entryGlobalName,
			shareScope: "default"
		};
	}
	return Object.assign({
		type: "var",
		name: key,
		shareScope: "default",
		entryGlobalName: key
	}, remote);
}
/**
* Tries to find the package.json's version of a shared package
* if `package.json` is not declared in `exports`
* @param {string} sharedName
* @returns {string | undefined}
*/
function searchPackageVersion(sharedName) {
	try {
		const sharedPath = createRequire(process.cwd()).resolve(sharedName);
		let potentialPackageJsonDir = path$1.dirname(sharedPath);
		const rootDir = path$1.parse(potentialPackageJsonDir).root;
		while (path$1.parse(potentialPackageJsonDir).base !== "node_modules" && potentialPackageJsonDir !== rootDir) {
			const potentialPackageJsonPath = path$1.join(potentialPackageJsonDir, "package.json");
			if (fs.existsSync(potentialPackageJsonPath)) {
				const potentialPackageJson = JSON.parse(fs.readFileSync(potentialPackageJsonPath, "utf-8"));
				if (typeof potentialPackageJson == "object" && potentialPackageJson !== null && typeof potentialPackageJson.version === "string" && potentialPackageJson.name === sharedName) return potentialPackageJson.version;
			}
			potentialPackageJsonDir = path$1.dirname(potentialPackageJsonDir);
		}
	} catch (_) {}
}
function inferVersionFromRequiredVersion(requiredVersion) {
	if (!requiredVersion) return void 0;
	return requiredVersion.match(/\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?/)?.[0];
}
function normalizeShareItem(key, shareItem) {
	let version;
	try {
		try {
			version = __require(path$1.join(removePathFromNpmPackage(key), "package.json")).version;
		} catch (e1) {
			try {
				version = __require(path$1.join(process.cwd(), "node_modules", removePathFromNpmPackage(key), "package.json")).version;
			} catch (e2) {
				version = searchPackageVersion(key);
				if (!version) console.error(e1);
			}
		}
	} catch (e) {
		console.error(`Unexpected error resolving version for ${key}:`, e);
	}
	if (typeof shareItem === "string") return {
		name: shareItem,
		version,
		scope: "default",
		from: "",
		shareConfig: {
			import: void 0,
			singleton: false,
			requiredVersion: version ? `^${version}` : "*"
		}
	};
	return {
		name: key,
		from: "",
		version: shareItem.version || inferVersionFromRequiredVersion(shareItem.requiredVersion) || version,
		scope: shareItem.shareScope || "default",
		shareConfig: {
			import: typeof shareItem === "object" ? shareItem.import : void 0,
			singleton: shareItem.singleton || false,
			requiredVersion: shareItem.requiredVersion || (version ? `^${version}` : "*"),
			strictVersion: !!shareItem.strictVersion
		}
	};
}
function normalizeShared(shared) {
	if (!shared) return {};
	const result = {};
	if (Array.isArray(shared)) {
		shared.forEach((key) => {
			result[key] = normalizeShareItem(key, key);
		});
		return result;
	}
	if (typeof shared === "object") Object.keys(shared).forEach((key) => {
		result[key] = normalizeShareItem(key, shared[key]);
	});
	return result;
}
function normalizeLibrary(library) {
	if (!library) return void 0;
	return library;
}
function normalizeManifest(manifest = false) {
	if (typeof manifest === "boolean") return manifest;
	return Object.assign({
		filePath: "",
		disableAssetsAnalyze: false,
		fileName: "mf-manifest.json"
	}, manifest);
}
let config;
function getNormalizeModuleFederationOptions() {
	return config;
}
function getNormalizeShareItem(key) {
	const options = getNormalizeModuleFederationOptions();
	return options.shared[key] || options.shared[removePathFromNpmPackage(key)] || options.shared[removePathFromNpmPackage(key) + "/"];
}
function normalizeModuleFederationOptions(options) {
	if (options.virtualModuleDir && options.virtualModuleDir.includes("/")) throw new Error(`Invalid virtualModuleDir: "${options.virtualModuleDir}". The virtualModuleDir option cannot contain slashes (/). Please use a single directory name like '__mf__virtual__your_app_name'.`);
	return config = {
		exposes: normalizeExposes(options.exposes),
		filename: options.filename || "remoteEntry-[hash]",
		library: normalizeLibrary(options.library),
		name: options.name,
		remotes: normalizeRemotes(options.remotes),
		runtime: options.runtime,
		shareScope: options.shareScope || "default",
		shared: normalizeShared(options.shared),
		runtimePlugins: options.runtimePlugins || [],
		implementation: options.implementation || __require.resolve("@module-federation/runtime"),
		manifest: normalizeManifest(options.manifest),
		dev: options.dev,
		dts: options.dts,
		getPublicPath: options.getPublicPath,
		publicPath: options.publicPath,
		shareStrategy: options.shareStrategy || "version-first",
		ignoreOrigin: options.ignoreOrigin || false,
		virtualModuleDir: options.virtualModuleDir || "__mf__virtual",
		hostInitInjectLocation: options.hostInitInjectLocation || "html",
		bundleAllCSS: options.bundleAllCSS || false,
		moduleParseTimeout: options.moduleParseTimeout || 10,
		moduleParseIdleTimeout: options.moduleParseIdleTimeout,
		varFilename: options.varFilename,
		target: options.target
	};
}
//#endregion
//#region src/utils/localSharedImportMap_temp.ts
/**
* https://github.com/module-federation/vite/issues/68
*/
function getLocalSharedImportMapPath_temp() {
	const { name } = getNormalizeModuleFederationOptions();
	return path.resolve(".__mf__temp", packageNameEncode(name), "localSharedImportMap");
}
function writeLocalSharedImportMap_temp(content) {
	createFile(getLocalSharedImportMapPath_temp() + ".js", "\n// Windows temporarily needs this file, https://github.com/module-federation/vite/issues/68\n" + content);
}
function createFile(filePath, content) {
	mkdirSync(path.dirname(filePath), { recursive: true });
	writeFileSync(filePath, content);
}
//#endregion
//#region src/utils/serializeRuntimeOptions.ts
/**
* Serializes a JavaScript object into a string of source code that can be evaluated.
* This function is used to create runtime plugin options without relying solely on JSON.stringify,
* allowing support for non-JSON types like RegExp, Date, Map, Set, and Functions.
* It also safely handles circular references.
*
* @param {Record<string, unknown>} options - The options object to serialize.
* @returns {string} The resulting JavaScript source code string.
*/
function serializeRuntimeOptions(options) {
	const seenObjects = /* @__PURE__ */ new WeakSet();
	/**
	* Recursive inner function to serialize any value into a source code string.
	*/
	function valueToCode(val) {
		if (val === null) return "null";
		const type = typeof val;
		if (type === "string") return JSON.stringify(val);
		if (type === "number" || type === "boolean") return String(val);
		if (type === "undefined") return "undefined";
		if (type === "symbol") {
			const desc = val.description ?? "";
			return `Symbol(${JSON.stringify(desc)})`;
		}
		if (type === "function") return val.toString();
		if (val instanceof Date) return `new Date(${JSON.stringify(val.toISOString())})`;
		if (val instanceof RegExp) return `new RegExp(${JSON.stringify(val.source)}, ${JSON.stringify(val.flags)})`;
		if (type === "object") {
			if (seenObjects.has(val)) return `"__circular__"`;
			seenObjects.add(val);
		}
		if (Array.isArray(val)) return `[${val.map(valueToCode).join(", ")}]`;
		if (val instanceof Map) return `new Map([${Array.from(val.entries()).map(([k, v]) => `[${valueToCode(k)}, ${valueToCode(v)}]`).join(", ")}])`;
		if (val instanceof Set) return `new Set([${Array.from(val.values()).map(valueToCode).join(", ")}])`;
		if (type === "object") {
			const properties = [];
			for (const key in val) if (Object.prototype.hasOwnProperty.call(val, key)) properties.push(`${JSON.stringify(key)}: ${valueToCode(val[key])}`);
			return `{${properties.join(", ")}}`;
		}
		return JSON.stringify(String(val));
	}
	const topLevelProps = [];
	for (const key in options) if (Object.prototype.hasOwnProperty.call(options, key)) topLevelProps.push(`${JSON.stringify(key)}: ${valueToCode(options[key])}`);
	return `{${topLevelProps.join(", ")}}`;
}
//#endregion
//#region src/utils/VirtualModule.ts
/**
* Initialize virtual module infrastructure BEFORE VirtualModule class is used.
* This must be called in the config hook to ensure the directory exists
* before Vite's optimization phase.
*/
function initVirtualModuleInfrastructure(root, virtualModuleDir = "__mf__virtual") {
	const virtualPackagePath = join(join(root, "node_modules"), virtualModuleDir);
	mkdirSync(virtualPackagePath, { recursive: true });
	writeFileSync(join(virtualPackagePath, "empty.js"), "");
	writeFileSync(join(virtualPackagePath, "package.json"), JSON.stringify({
		name: virtualModuleDir,
		main: "empty.js"
	}));
}
let rootDir;
function findNodeModulesDir(root = process.cwd()) {
	let currentDir = root;
	while (currentDir !== parse(currentDir).root) {
		const nodeModulesPath = join(currentDir, "node_modules");
		if (existsSync(nodeModulesPath)) return nodeModulesPath;
		currentDir = dirname(currentDir);
	}
	return "";
}
let cachedNodeModulesDir;
function getNodeModulesDir() {
	if (!cachedNodeModulesDir) cachedNodeModulesDir = findNodeModulesDir(rootDir);
	return cachedNodeModulesDir;
}
function getSuffix(name) {
	const base = basename(name);
	const dotIndex = base.lastIndexOf(".");
	if (dotIndex > 0 && dotIndex < base.length - 1) return base.slice(dotIndex);
	return ".js";
}
const patternMap = {};
const cacheMap = {};
/**
* Physically generate files as virtual modules under node_modules/__mf__virtual/*
*/
function assertModuleFound(tag, str = "") {
	const module = VirtualModule.findModule(tag, str);
	if (!module) throw new Error(`Module Federation shared module '${str}' not found. Please ensure it's installed as a dependency in your package.json.`);
	return module;
}
var VirtualModule = class {
	/**
	* Set the root path for finding node_modules
	* @param root - Root path
	*/
	static setRoot(root) {
		rootDir = root;
		cachedNodeModulesDir = void 0;
	}
	/**
	* Ensure virtual package directory exists
	*/
	static ensureVirtualPackageExists() {
		const nodeModulesDir = getNodeModulesDir();
		const { virtualModuleDir } = getNormalizeModuleFederationOptions();
		const virtualPackagePath = resolve(nodeModulesDir, virtualModuleDir);
		mkdirSync(virtualPackagePath, { recursive: true });
		writeFileSync(resolve(virtualPackagePath, "empty.js"), "");
		writeFileSync(resolve(virtualPackagePath, "package.json"), JSON.stringify({
			name: virtualModuleDir,
			main: "empty.js"
		}));
	}
	static findModule(tag, str = "") {
		if (!patternMap[tag]) patternMap[tag] = new RegExp(`(.*${packageNameEncode(tag)}(.+?)${packageNameEncode(tag)}.*)`);
		const moduleName = (str.match(patternMap[tag]) || [])[2];
		if (moduleName) return cacheMap[tag][packageNameDecode(moduleName)];
	}
	constructor(name, tag = "__mf_v__", suffix = "") {
		this.inited = false;
		this.name = name;
		this.tag = tag;
		this.suffix = suffix || getSuffix(name);
		if (!cacheMap[this.tag]) cacheMap[this.tag] = {};
		cacheMap[this.tag][this.name] = this;
	}
	getPath() {
		return resolve(getNodeModulesDir(), this.getImportId());
	}
	getImportId() {
		const { name: mfName, virtualModuleDir } = getNormalizeModuleFederationOptions();
		return `${virtualModuleDir}/${packageNameEncode(`${mfName}${this.tag}${this.name}${this.tag}`)}${this.suffix}`;
	}
	writeSync(code, force) {
		if (!force && this.inited) return;
		if (!this.inited) this.inited = true;
		const path = this.getPath();
		mkdirSync(dirname(path), { recursive: true });
		writeFileSync(path, code);
	}
	write(code) {
		const path = this.getPath();
		mkdirSync(dirname(path), { recursive: true });
		writeFile(path, code, function() {});
	}
};
//#endregion
//#region src/virtualModules/virtualExposes.ts
function getVirtualExposesId(options) {
	return `virtual:mf-exposes:${`${options.name}__${options.filename}`.replace(/[^a-zA-Z0-9_-]/g, "_")}`;
}
function generateExposes(options) {
	return `
    export default {
    ${Object.keys(options.exposes).map((key) => {
		return `
        ${JSON.stringify(key)}: async () => {
          const importModule = await import(${JSON.stringify(options.exposes[key].import)})
          const exportModule = {}
          Object.assign(exportModule, importModule)
          Object.defineProperty(exportModule, "__esModule", {
            value: true,
            enumerable: false
          })
          return exportModule
        }
      `;
	}).join(",")}
  }
  `;
}
//#endregion
//#region src/virtualModules/virtualRuntimeInitStatus.ts
const virtualRuntimeInitStatus = new VirtualModule("runtimeInit");
function getRuntimeInitGlobalKey() {
	return `__mf_init__${virtualRuntimeInitStatus.getImportId()}__`;
}
function getRuntimeInitBootstrapCode() {
	return `
const globalKey = ${JSON.stringify(getRuntimeInitGlobalKey())};
if (!globalThis[globalKey]) {
  let initResolve, initReject;
  const initPromise = new Promise((re, rj) => {
    initResolve = re;
    initReject = rj;
  });
  globalThis[globalKey] = {
    initPromise,
    initResolve,
    initReject,
  };
  if (typeof window === 'undefined') {
    initResolve({
      loadRemote: function() { return Promise.resolve(undefined); },
      loadShare: function() { return Promise.resolve(undefined); },
    });
  }
}
`;
}
function getRuntimeInitPromiseBootstrapCode() {
	return `
const __mfPromiseGlobalKey = ${JSON.stringify(getRuntimeInitGlobalKey())};
let __mfPromiseState = globalThis[__mfPromiseGlobalKey];
if (!__mfPromiseState) {
  let initResolve, initReject;
  const initPromise = new Promise((re, rj) => {
    initResolve = re;
    initReject = rj;
  });
  __mfPromiseState = globalThis[__mfPromiseGlobalKey] = {
    initPromise,
    initResolve,
    initReject,
  };
  if (typeof window === 'undefined') {
    initResolve({
      loadRemote: function() { return Promise.resolve(undefined); },
      loadShare: function() { return Promise.resolve(undefined); },
    });
  }
}
const initPromise = __mfPromiseState.initPromise;
`;
}
function getRuntimeInitResolveBootstrapCode() {
	return `
const __mfResolveGlobalKey = ${JSON.stringify(getRuntimeInitGlobalKey())};
let __mfResolveState = globalThis[__mfResolveGlobalKey];
if (!__mfResolveState) {
  let initResolve, initReject;
  const initPromise = new Promise((re, rj) => {
    initResolve = re;
    initReject = rj;
  });
  __mfResolveState = globalThis[__mfResolveGlobalKey] = {
    initPromise,
    initResolve,
    initReject,
  };
  if (typeof window === 'undefined') {
    initResolve({
      loadRemote: function() { return Promise.resolve(undefined); },
      loadShare: function() { return Promise.resolve(undefined); },
    });
  }
}
const initResolve = __mfResolveState.initResolve;
`;
}
function writeRuntimeInitStatus(command) {
	getRuntimeInitGlobalKey();
	const exportStatement = command === "build" ? `const { initPromise, initResolve, initReject } = globalThis[globalKey];
export { initPromise, initResolve, initReject };` : `module.exports = globalThis[globalKey];`;
	virtualRuntimeInitStatus.writeSync(`
${getRuntimeInitBootstrapCode()}
${exportStatement}
`);
}
//#endregion
//#region src/virtualModules/virtualRemotes.ts
const cacheRemoteMap = {};
const LOAD_REMOTE_TAG = "__loadRemote__";
function getRemoteVirtualModule(remote, command, isRolldown) {
	if (!cacheRemoteMap[remote]) {
		cacheRemoteMap[remote] = new VirtualModule(remote, LOAD_REMOTE_TAG, isRolldown ? ".mjs" : ".js");
		cacheRemoteMap[remote].writeSync(generateRemotes(remote, command, isRolldown));
	}
	return cacheRemoteMap[remote];
}
const usedRemotesMap = {};
function addUsedRemote(remoteKey, remoteModule) {
	if (!usedRemotesMap[remoteKey]) usedRemotesMap[remoteKey] = /* @__PURE__ */ new Set();
	usedRemotesMap[remoteKey].add(remoteModule);
}
function getUsedRemotesMap() {
	return usedRemotesMap;
}
function generateRemotes(id, command, isRolldown) {
	const useESM = command === "build" || isRolldown;
	const importLine = command === "build" ? getRuntimeInitPromiseBootstrapCode() : useESM ? `${getRuntimeInitBootstrapCode()}
    const { initPromise } = globalThis[globalKey];` : `const {initPromise} = require("${virtualRuntimeInitStatus.getImportId()}")`;
	const awaitOrPlaceholder = useESM ? "await " : "/*mf top-level-await placeholder replacement mf*/";
	const exportLine = command === "serve" && useESM ? "export default exportModule.default ?? exportModule" : useESM ? "export default exportModule" : "module.exports = exportModule";
	return `
    ${importLine}
    const res = initPromise.then(runtime => runtime.loadRemote(${JSON.stringify(id)}))
    const exportModule = ${awaitOrPlaceholder}initPromise.then(_ => res)
    ${exportLine}
  `;
}
//#endregion
//#region src/virtualModules/virtualShared_preBuild.ts
/**
* Even the resolveId hook cannot interfere with vite pre-build,
* and adding query parameter virtual modules will also fail.
* You can only proxy to the real file through alias
*/
/**
* shared will be proxied:
* 1. __prebuild__: export shareModule (pre-built source code of modules such as vue, react, etc.)
* 2. __loadShare__: load shareModule (mfRuntime.loadShare('vue'))
*/
function getPackageNamedExports(pkg) {
	try {
		const mod = createRequire$1(new URL("file://" + process.cwd() + "/package.json"))(pkg);
		return Object.keys(mod).filter((k) => k !== "default" && k !== "__esModule" && /^[A-Za-z_$][A-Za-z0-9_$]*$/.test(k));
	} catch {
		return [];
	}
}
function getLocalProviderImportPath(pkg) {
	try {
		const resolved = createRequire$1(new URL("file://" + process.cwd() + "/package.json")).resolve(pkg);
		return resolved.includes("/node_modules/") || resolved.includes("\\node_modules\\") ? void 0 : resolved;
	} catch {
		return;
	}
}
const preBuildCacheMap = {};
const PREBUILD_TAG = "__prebuild__";
function writePreBuildLibPath(pkg) {
	if (!preBuildCacheMap[pkg]) preBuildCacheMap[pkg] = new VirtualModule(pkg, PREBUILD_TAG);
	preBuildCacheMap[pkg].writeSync("");
}
function getPreBuildLibImportId(pkg) {
	if (!preBuildCacheMap[pkg]) preBuildCacheMap[pkg] = new VirtualModule(pkg, PREBUILD_TAG);
	return preBuildCacheMap[pkg].getImportId();
}
const LOAD_SHARE_TAG = "__loadShare__";
const loadShareCacheMap = {};
function getLoadShareModulePath(pkg, isRolldown, command) {
	if (!loadShareCacheMap[pkg]) loadShareCacheMap[pkg] = new VirtualModule(pkg, LOAD_SHARE_TAG, isRolldown || command === "build" ? ".mjs" : ".js");
	return loadShareCacheMap[pkg].getPath();
}
function writeLoadShareModule(pkg, shareItem, command, isRolldown) {
	if (!loadShareCacheMap[pkg]) loadShareCacheMap[pkg] = new VirtualModule(pkg, LOAD_SHARE_TAG, isRolldown || command === "build" ? ".mjs" : ".js");
	const useESM = command === "build" || isRolldown;
	const importLine = command === "build" ? getRuntimeInitPromiseBootstrapCode() : useESM ? `${getRuntimeInitBootstrapCode()}
    const { initPromise } = globalThis[globalKey];` : `const {initPromise} = require("${virtualRuntimeInitStatus.getImportId()}")`;
	const awaitOrPlaceholder = useESM ? "await " : "/*mf top-level-await placeholder replacement mf*/";
	const useSsrProviderFallback = hasPackageDependency("vinext") && command === "build" && pkg === "react";
	const providerImportId = getLocalProviderImportPath(pkg) || getPreBuildLibImportId(pkg);
	const namedExports = getPackageNamedExports(pkg);
	let exportLine;
	if (namedExports.length > 0) {
		const destructure = `const { ${namedExports.map((name, i) => `${name}: __mf_${i}`).join(", ")} } = exportModule;`;
		const namedExportLine = `export { ${namedExports.map((name, i) => `__mf_${i} as ${name}`).join(", ")} };`;
		exportLine = useESM ? `export default exportModule;\n    ${destructure}\n    ${namedExportLine}` : `module.exports = exportModule;\n    ${destructure}\n    Object.assign(module.exports, { ${namedExports.map((name, i) => `"${name}": __mf_${i}`).join(", ")} });`;
	} else exportLine = useESM ? `export default exportModule\n    export * from ${JSON.stringify(getPreBuildLibImportId(pkg))}` : "module.exports = exportModule";
	loadShareCacheMap[pkg].writeSync(`
    import ${JSON.stringify(getPreBuildLibImportId(pkg))};
    ${command !== "build" ? `;() => import(${JSON.stringify(pkg)}).catch(() => {});` : ""}
    ${importLine}
    ${useSsrProviderFallback ? `const providerModulePromise = typeof window === "undefined"
      ? import(${JSON.stringify(providerImportId)})
      : undefined` : ""}
    const res = initPromise.then(runtime => runtime.loadShare(${JSON.stringify(pkg)}, {
      customShareInfo: {shareConfig:{
        singleton: ${shareItem.shareConfig.singleton},
        strictVersion: ${shareItem.shareConfig.strictVersion},
        requiredVersion: ${JSON.stringify(shareItem.shareConfig.requiredVersion)}
      }}
    }))
    const exportModule = ${useSsrProviderFallback ? `(typeof window === "undefined"
      ? ((await providerModulePromise)?.default ?? await providerModulePromise)
      : ${awaitOrPlaceholder}res.then((factory) => (typeof factory === "function" ? factory() : factory)))` : `${awaitOrPlaceholder}res.then((factory) => (typeof factory === "function" ? factory() : factory))`}
    ${exportLine}
  `);
}
//#endregion
//#region src/virtualModules/virtualRemoteEntry.ts
let usedShares = /* @__PURE__ */ new Set();
function getUsedShares() {
	return usedShares;
}
function addUsedShares(pkg) {
	usedShares.add(pkg);
}
new VirtualModule("localSharedImportMap");
function getLocalSharedImportMapPath() {
	return getLocalSharedImportMapPath_temp();
}
let prevLocalSharedImportMapContent;
function writeLocalSharedImportMap() {
	const nextContent = generateLocalSharedImportMap();
	if (prevLocalSharedImportMapContent !== nextContent) {
		prevLocalSharedImportMapContent = nextContent;
		writeLocalSharedImportMap_temp(nextContent);
	}
}
function generateLocalSharedImportMap() {
	const isVinext = hasPackageDependency("vinext");
	const options = getNormalizeModuleFederationOptions();
	return `
    import {loadShare} from "@module-federation/runtime";
    const importMap = {
      ${Array.from(getUsedShares()).sort().map((pkg) => {
		const shareItem = getNormalizeShareItem(pkg);
		return `
        ${JSON.stringify(pkg)}: async () => {
          ${shareItem?.shareConfig.import === false ? `throw new Error(\`Shared module '\${${JSON.stringify(pkg)}}' must be provided by host\`);` : isVinext && pkg === "react" ? `let pkg = await import("react");
            return pkg;` : `let pkg = await import("${getPreBuildLibImportId(pkg)}");
            return pkg;`}
        }
      `;
	}).join(",")}
    }
      const usedShared = {
      ${Array.from(getUsedShares()).sort().map((key) => {
		const shareItem = getNormalizeShareItem(key);
		if (!shareItem) return null;
		return `
          ${JSON.stringify(key)}: {
            name: ${JSON.stringify(key)},
            version: ${JSON.stringify(shareItem.version)},
            scope: [${JSON.stringify(shareItem.scope)}],
            loaded: false,
            from: ${JSON.stringify(options.name)},
            async get () {
              if (${shareItem.shareConfig.import === false}) {
                throw new Error(\`Shared module '\${${JSON.stringify(key)}}' must be provided by host\`);
              }
              usedShared[${JSON.stringify(key)}].loaded = true
              const {${JSON.stringify(key)}: pkgDynamicImport} = importMap
              const res = await pkgDynamicImport()
              const exportModule = ${JSON.stringify(isVinext)} && ${JSON.stringify(key)} === "react"
                ? (res?.default ?? res)
                : {...res}
              // All npm packages pre-built by vite will be converted to esm
              Object.defineProperty(exportModule, "__esModule", {
                value: true,
                enumerable: false
              })
              return function () {
                return exportModule
              }
            },
            shareConfig: {
              singleton: ${shareItem.shareConfig.singleton},
              requiredVersion: ${JSON.stringify(shareItem.shareConfig.requiredVersion)},
              ${shareItem.shareConfig.import === false ? "import: false," : ""}
            }
          }
        `;
	}).filter((x) => x !== null).join(",")}
    }
      const usedRemotes = [${Object.keys(getUsedRemotesMap()).map((key) => {
		const remote = options.remotes[key];
		if (!remote) return null;
		return `
                {
                  entryGlobalName: ${JSON.stringify(remote.entryGlobalName)},
                  name: ${JSON.stringify(remote.name)},
                  type: ${JSON.stringify(remote.type)},
                  entry: ${JSON.stringify(remote.entry)},
                  shareScope: ${JSON.stringify(remote.shareScope) ?? "default"},
                }
          `;
	}).filter((x) => x !== null).join(",")}
      ]
      export {
        usedShared,
        usedRemotes
      }
      `;
}
const REMOTE_ENTRY_ID = "virtual:mf-REMOTE_ENTRY_ID";
function getRemoteEntryId(options) {
	return `${REMOTE_ENTRY_ID}:${`${options.name}__${options.filename}`.replace(/[^a-zA-Z0-9_-]/g, "_")}`;
}
function generateRemoteEntry(options, virtualExposesId = getVirtualExposesId(options), command = "build") {
	const pluginImportNames = options.runtimePlugins.map((p, i) => {
		if (typeof p === "string") return [
			`$runtimePlugin_${i}`,
			`import $runtimePlugin_${i} from "${p}";`,
			`undefined`
		];
		else return [
			`$runtimePlugin_${i}`,
			`import $runtimePlugin_${i} from "${p[0]}";`,
			serializeRuntimeOptions(p[1])
		];
	});
	return `
  import {init as runtimeInit, loadRemote} from "@module-federation/runtime";
  ${pluginImportNames.map((item) => item[1]).join("\n")}
  ${command === "build" ? getRuntimeInitResolveBootstrapCode() : getRuntimeInitBootstrapCode() + "\n  const { initResolve } = globalThis[globalKey];"}
  const initTokens = {}
  const shareScopeName = ${JSON.stringify(options.shareScope)}
  const mfName = ${JSON.stringify(options.name)}
  let localSharedImportMapPromise
  let exposesMapPromise

  async function getLocalSharedImportMap() {
    localSharedImportMapPromise ??= import("${getLocalSharedImportMapPath()}")
    return localSharedImportMapPromise
  }

  async function getExposesMap() {
    exposesMapPromise ??= import("${virtualExposesId}").then((mod) => mod.default ?? mod)
    return exposesMapPromise
  }

  async function init(shared = {}, initScope = []) {
    const {usedShared, usedRemotes} = await getLocalSharedImportMap()
    const initRes = runtimeInit({
      name: mfName,
      remotes: usedRemotes,
      shared: usedShared,
      plugins: [${pluginImportNames.map((item) => `${item[0]}(${item[2]})`).join(", ")}],
      ${options.shareStrategy ? `shareStrategy: '${options.shareStrategy}'` : ""}
    });
    // handling circular init calls
    var initToken = initTokens[shareScopeName];
    if (!initToken)
      initToken = initTokens[shareScopeName] = { from: mfName };
    if (initScope.indexOf(initToken) >= 0) return;
    initScope.push(initToken);
    initRes.initShareScopeMap('${options.shareScope}', shared);
    initResolve(initRes)
    try {
      await Promise.all(await initRes.initializeSharing('${options.shareScope}', {
        strategy: '${options.shareStrategy}',
        from: "build",
        initScope
      }));
    } catch (e) {
      console.error(e)
    }
    return initRes
  }

  async function getExposes(moduleName) {
    const exposesMap = await getExposesMap()
    if (!(moduleName in exposesMap)) throw new Error(\`Module \${moduleName} does not exist in container.\`)
    return (exposesMap[moduleName])().then(res => () => res)
  }
  export {
      init,
      getExposes as get
  }
  `;
}
const hostAutoInitModule = new VirtualModule("hostAutoInit", "__H_A_I__");
function writeHostAutoInit(remoteEntryId = REMOTE_ENTRY_ID) {
	hostAutoInitModule.writeSync(`
    const remoteEntry = await import("${remoteEntryId}");
    await remoteEntry.init();
    `);
}
function getHostAutoInitImportId() {
	return hostAutoInitModule.getImportId();
}
function getHostAutoInitPath() {
	return hostAutoInitModule.getPath();
}
//#endregion
//#region src/virtualModules/index.ts
function initVirtualModules(command, remoteEntryId) {
	writeLocalSharedImportMap();
	writeHostAutoInit(remoteEntryId);
	writeRuntimeInitStatus(command);
}
//#endregion
//#region src/utils/bundleHelpers.ts
/**
* Resolve the local alias for a non-inlineable proxy binding.
* If Rollup's deconflict renamed the alias but didn't update references
* in the code body, fall back to proxyLocal so they stay in sync.
*/
function resolveProxyAlias(binding, proxyLocal, code, fullImport, claimedLocals = /* @__PURE__ */ new Set()) {
	const codeWithoutImport = code.replace(fullImport, "");
	const escapedLocal = binding.local.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
	const localUsedInCode = new RegExp(`\\b${escapedLocal}\\b`).test(codeWithoutImport);
	const claimedImportLocals = /* @__PURE__ */ new Set();
	const importRe = /import\s*\{([^}]+)\}\s*from\s*["'][^"']+["']\s*;?/g;
	let match;
	while ((match = importRe.exec(codeWithoutImport)) !== null) for (const spec of match[1].split(",")) {
		const parts = spec.trim().split(/\s+as\s+/);
		claimedImportLocals.add((parts[1] || parts[0]).trim());
	}
	const local = !localUsedInCode && !claimedLocals.has(proxyLocal) && !claimedImportLocals.has(proxyLocal) ? proxyLocal : binding.local;
	return {
		imported: binding.imported,
		local
	};
}
function findRemoteEntryFile(filename, bundle) {
	for (const [_, fileData] of Object.entries(bundle)) if (filename.replace(/[\[\]]/g, "_").replace(/\.[^/.]+$/, "") === fileData.name || fileData.name === "remoteEntry") return fileData.fileName;
}
//#endregion
//#region src/utils/cssModuleHelpers.ts
const ASSET_TYPES = ["js", "css"];
const LOAD_TIMINGS = ["sync", "async"];
const JS_EXTENSIONS = [
	".ts",
	".tsx",
	".jsx",
	".mjs",
	".cjs"
];
/**
* Creates an empty asset map structure for tracking JS and CSS assets
* @returns Initialized asset map with sync/async arrays for JS and CSS
*/
const createEmptyAssetMap = () => ({
	js: {
		sync: [],
		async: []
	},
	css: {
		sync: [],
		async: []
	}
});
/**
* Tracks an asset in the preload map with deduplication
* @param map - The preload map to update
* @param key - The module key to track under
* @param fileName - The asset filename to track
* @param isAsync - Whether the asset is loaded async
* @param type - The asset type ('js' or 'css')
*/
const trackAsset = (map, key, fileName, isAsync, type) => {
	if (!map[key]) map[key] = createEmptyAssetMap();
	const target = isAsync ? map[key][type].async : map[key][type].sync;
	if (!target.includes(fileName)) target.push(fileName);
};
/**
* Checks if a file is a CSS file by extension
* @param fileName - The filename to check
* @returns True if file has a CSS extension (.css, .scss, .less)
*/
const isCSSFile = (fileName) => {
	return fileName.endsWith(".css") || fileName.endsWith(".scss") || fileName.endsWith(".less");
};
/**
* Collects all CSS assets from the bundle
* @param bundle - The Rollup output bundle
* @returns Set of CSS asset filenames
*/
const collectCssAssets = (bundle) => {
	const cssAssets = /* @__PURE__ */ new Set();
	for (const [fileName, fileData] of Object.entries(bundle)) if (fileData.type === "asset" && isCSSFile(fileName)) cssAssets.add(fileName);
	return cssAssets;
};
/**
* Checks if a chunk contains CSS modules (e.g. .css, .vanilla.css, .scss, .less)
* by scanning its module list
*/
const chunkContainsCssModules = (modules) => {
	for (const modulePath of Object.keys(modules)) if (isCSSFile(modulePath)) return true;
	return false;
};
/**
* Processes module assets and tracks them in the files map
* @param bundle - The Rollup output bundle
* @param filesMap - The preload map to populate
* @param moduleMatcher - Function that matches module paths to keys
*/
const processModuleAssets = (bundle, filesMap, moduleMatcher) => {
	const bundleCssAssets = collectCssAssets(bundle);
	for (const [fileName, fileData] of Object.entries(bundle)) {
		if (fileData.type !== "chunk") continue;
		if (!fileData.modules) continue;
		for (const modulePath of Object.keys(fileData.modules)) {
			const matchKey = moduleMatcher(modulePath);
			if (!matchKey) continue;
			trackAsset(filesMap, matchKey, fileName, false, "js");
			let foundCssViaMetadata = false;
			if (fileData.viteMetadata?.importedCss?.size) for (const cssFile of fileData.viteMetadata.importedCss) {
				trackAsset(filesMap, matchKey, cssFile, false, "css");
				foundCssViaMetadata = true;
			}
			if (!foundCssViaMetadata && chunkContainsCssModules(fileData.modules)) for (const cssAsset of bundleCssAssets) trackAsset(filesMap, matchKey, cssAsset, false, "css");
			if (fileData.dynamicImports) for (const dynamicImport of fileData.dynamicImports) {
				if (!bundle[dynamicImport]) continue;
				trackAsset(filesMap, matchKey, dynamicImport, true, isCSSFile(dynamicImport) ? "css" : "js");
			}
		}
	}
};
/**
* Deduplicates assets in the files map
* @param filesMap - The preload map to deduplicate
* @returns New deduplicated preload map
*/
const deduplicateAssets = (filesMap) => {
	const result = {};
	for (const [key, assetMaps] of Object.entries(filesMap)) {
		result[key] = createEmptyAssetMap();
		for (const type of ASSET_TYPES) for (const timing of LOAD_TIMINGS) result[key][type][timing] = Array.from(new Set(assetMaps[type][timing]));
	}
	return result;
};
/**
* Builds a mapping between module files and their share keys
* @param shareKeys - Set of share keys to map
* @param resolveFn - Function to resolve module paths
* @returns Map of file paths to their corresponding share keys
*/
const buildFileToShareKeyMap = async (shareKeys, resolveFn) => {
	const fileToShareKey = /* @__PURE__ */ new Map();
	const resolutions = await Promise.all(Array.from(shareKeys).map((shareKey) => resolveFn(getPreBuildLibImportId(shareKey)).then((resolution) => ({
		shareKey,
		file: resolution?.id?.split("?")[0]
	})).catch(() => null)));
	for (const resolution of resolutions) if (resolution?.file) fileToShareKey.set(resolution.file, resolution.shareKey);
	return fileToShareKey;
};
//#endregion
//#region src/utils/publicPath.ts
/**
* Resolves the public path for remote entries
* @param options - Module Federation options
* @param viteBase - Vite's base config value
* @param originalBase - Original base config before any transformations
* @returns The resolved public path
*/
function resolvePublicPath(options, viteBase, originalBase) {
	if (options.publicPath) return options.publicPath;
	if (originalBase === "") return "auto";
	if (viteBase) return viteBase.replace(/\/?$/, "/");
	return "auto";
}
//#endregion
//#region src/plugins/pluginMFManifest.ts
const Manifest = () => {
	const mfOptions = getNormalizeModuleFederationOptions();
	const { name, filename, getPublicPath, manifest: manifestOptions, varFilename } = mfOptions;
	let mfManifestName = "";
	if (manifestOptions === true) mfManifestName = "mf-manifest.json";
	if (typeof manifestOptions !== "boolean") mfManifestName = path$1.join(manifestOptions?.filePath || "", manifestOptions?.fileName || "");
	let root;
	let remoteEntryFile;
	let publicPath;
	let _command;
	let _originalConfigBase;
	let viteConfig;
	/**
	* Adds global CSS assets to all module exports
	* @param filesMap - The preload map to update
	* @param cssAssets - Set of CSS asset filenames to add
	*/
	const addCssAssetsToAllExports = (filesMap, cssAssets) => {
		Object.keys(filesMap).forEach((key) => {
			cssAssets.forEach((cssAsset) => {
				trackAsset(filesMap, key, cssAsset, false, "css");
			});
		});
	};
	return [{
		name: "module-federation-manifest",
		apply: "serve",
		configResolved(config) {
			viteConfig = config;
		},
		configureServer(server) {
			server.middlewares.use((req, res, next) => {
				if (!mfManifestName) {
					next();
					return;
				}
				if (req.url?.replace(/\?.*/, "") === (viteConfig.base + mfManifestName).replace(/^\/?/, "/")) {
					res.setHeader("Content-Type", "application/json");
					res.setHeader("Access-Control-Allow-Origin", "*");
					res.end(JSON.stringify({
						...generateMFManifest({}),
						id: name,
						name,
						metaData: {
							name,
							type: "app",
							buildInfo: {
								buildVersion: "1.0.0",
								buildName: name
							},
							remoteEntry: {
								name: filename,
								path: "",
								type: "module"
							},
							ssrRemoteEntry: {
								name: filename,
								path: "",
								type: "module"
							},
							varRemoteEntry: varFilename ? {
								name: varFilename,
								path: "",
								type: "var"
							} : void 0,
							types: {
								path: "",
								name: ""
							},
							globalName: name,
							pluginVersion: "0.2.5",
							publicPath
						}
					}));
				} else next();
			});
		}
	}, {
		name: "module-federation-manifest",
		enforce: "post",
		config(config, { command }) {
			if (!config.build) config.build = {};
			if (!config.build.manifest) config.build.manifest = config.build.manifest || !!manifestOptions;
			_command = command;
			_originalConfigBase = config.base;
		},
		configResolved(config) {
			root = config.root;
			let base = config.base;
			if (_command === "serve") base = (config.server.origin || "") + config.base;
			publicPath = resolvePublicPath(mfOptions, base, _originalConfigBase);
		},
		async generateBundle(options, bundle) {
			if (!mfManifestName) return;
			let filesMap = {};
			const foundRemoteEntryFile = findRemoteEntryFile(mfOptions.filename, bundle);
			if (foundRemoteEntryFile) remoteEntryFile = foundRemoteEntryFile;
			const allCssAssets = mfOptions.bundleAllCSS ? collectCssAssets(bundle) : /* @__PURE__ */ new Set();
			const exposesModules = Object.keys(mfOptions.exposes).map((item) => mfOptions.exposes[item].import);
			processModuleAssets(bundle, filesMap, (modulePath) => {
				const absoluteModulePath = path$1.resolve(root, modulePath);
				return exposesModules.find((exposeModule) => {
					const exposePath = path$1.resolve(root, exposeModule);
					if (absoluteModulePath === exposePath) return true;
					const getPathWithoutKnownExt = (filePath) => {
						const ext = path$1.extname(filePath);
						return JS_EXTENSIONS.includes(ext) ? path$1.join(path$1.dirname(filePath), path$1.basename(filePath, ext)) : filePath;
					};
					return getPathWithoutKnownExt(absoluteModulePath) === getPathWithoutKnownExt(exposePath);
				});
			});
			const fileToShareKey = await buildFileToShareKeyMap(getUsedShares(), this.resolve.bind(this));
			processModuleAssets(bundle, filesMap, (modulePath) => fileToShareKey.get(modulePath));
			if (mfOptions.bundleAllCSS) addCssAssetsToAllExports(filesMap, allCssAssets);
			filesMap = deduplicateAssets(filesMap);
			this.emitFile({
				type: "asset",
				fileName: mfManifestName,
				source: JSON.stringify(generateMFManifest(filesMap))
			});
		}
	}];
	/**
	* Generates the final manifest JSON structure
	* @param preloadMap - Map of module assets to include
	* @returns Complete manifest object
	*/
	function generateMFManifest(preloadMap) {
		const options = getNormalizeModuleFederationOptions();
		const { name, varFilename } = options;
		const remoteEntry = {
			name: remoteEntryFile,
			path: "",
			type: "module"
		};
		const varRemoteEntry = varFilename ? {
			name: varFilename,
			path: "",
			type: "module"
		} : void 0;
		const remotes = Array.from(Object.entries(getUsedRemotesMap())).flatMap(([remoteKey, modules]) => Array.from(modules).map((moduleKey) => ({
			federationContainerName: options.remotes[remoteKey].entry,
			moduleName: moduleKey.replace(remoteKey, "").replace("/", ""),
			alias: remoteKey,
			entry: "*"
		})));
		const shared = Array.from(getUsedShares()).map((shareKey) => {
			const shareItem = getNormalizeShareItem(shareKey);
			const assets = preloadMap[shareKey] || createEmptyAssetMap();
			return {
				id: `${name}:${shareKey}`,
				name: shareKey,
				version: shareItem.version,
				requiredVersion: shareItem.shareConfig.requiredVersion,
				assets: {
					js: {
						async: assets.js.async,
						sync: assets.js.sync
					},
					css: {
						async: assets.css.async,
						sync: assets.css.sync
					}
				}
			};
		}).filter(Boolean);
		const exposes = Object.entries(options.exposes).map(([key, value]) => {
			const formatKey = key.replace("./", "");
			const assets = preloadMap[value.import] || createEmptyAssetMap();
			return {
				id: `${name}:${formatKey}`,
				name: formatKey,
				assets: {
					js: {
						async: assets.js.async,
						sync: assets.js.sync
					},
					css: {
						async: assets.css.async,
						sync: assets.css.sync
					}
				},
				path: key
			};
		}).filter(Boolean);
		return {
			id: name,
			name,
			metaData: {
				name,
				type: "app",
				buildInfo: {
					buildVersion: "1.0.0",
					buildName: name
				},
				remoteEntry,
				ssrRemoteEntry: remoteEntry,
				varRemoteEntry,
				types: {
					path: "",
					name: ""
				},
				globalName: name,
				pluginVersion: "0.2.5",
				...!!getPublicPath ? { getPublicPath } : { publicPath }
			},
			shared,
			remotes,
			exposes
		};
	}
};
//#endregion
//#region src/plugins/pluginModuleParseEnd.ts
let _resolve, _parseTimeout;
const promise = new Promise((resolve, reject) => {
	_resolve = (v) => {
		clearTimeout(_parseTimeout);
		_parseTimeout = null;
		resolve(v);
	};
});
function setParseTimeout(timeout) {
	if (!_parseTimeout) _parseTimeout = setTimeout(() => {
		console.warn(`Parse timeout (${timeout}s) - forcing resolve`);
		_resolve(1);
	}, timeout * 1e3);
}
function resetIdleTimeout(timeout) {
	clearTimeout(_parseTimeout);
	_parseTimeout = setTimeout(() => {
		console.warn(`moduleParseIdleTimeout: no module activity for ${timeout}s, forcing resolve. Some shared/remote dependencies may be missing. Consider increasing moduleParseIdleTimeout.`);
		_resolve(1);
	}, timeout * 1e3);
}
let parsePromise = promise;
let exposesParseEnd = false;
const parseStartSet = /* @__PURE__ */ new Set();
const parseEndSet = /* @__PURE__ */ new Set();
function pluginModuleParseEnd_default(excludeFn, options) {
	const idleTimeout = options.moduleParseIdleTimeout;
	return [
		{
			name: "_",
			apply: "serve",
			config() {
				_resolve(1);
			}
		},
		{
			enforce: "pre",
			name: "parseStart",
			apply: "build",
			buildStart() {
				if (idleTimeout) resetIdleTimeout(idleTimeout);
				else setParseTimeout(options.moduleParseTimeout);
			},
			load(id) {
				if (excludeFn(id)) return;
				parseStartSet.add(id);
			}
		},
		{
			enforce: "post",
			name: "parseEnd",
			apply: "build",
			moduleParsed(module) {
				const id = module.id;
				if (id === options.virtualExposesId) exposesParseEnd = true;
				if (idleTimeout) resetIdleTimeout(idleTimeout);
				if (excludeFn(id)) return;
				parseEndSet.add(id);
				if (exposesParseEnd && parseStartSet.size === parseEndSet.size) _resolve(1);
			}
		}
	];
}
//#endregion
//#region src/plugins/pluginProxyRemoteEntry.ts
const filter = createFilter();
function pluginProxyRemoteEntry_default({ options, remoteEntryId, virtualExposesId }) {
	let viteConfig, _command;
	return {
		name: "proxyRemoteEntry",
		enforce: "post",
		configResolved(config) {
			viteConfig = config;
		},
		config(config, { command }) {
			_command = command;
		},
		async buildStart() {
			if (_command !== "build") return;
			for (const expose of Object.values(options.exposes)) {
				const resolved = await this.resolve(expose.import);
				if (resolved) this.emitFile({
					type: "chunk",
					id: resolved.id
				});
			}
		},
		async resolveId(id, importer) {
			if (id === remoteEntryId) return remoteEntryId;
			if (id === virtualExposesId) return virtualExposesId;
			if (_command === "serve" && id.includes(getHostAutoInitPath())) return id;
			if (importer === remoteEntryId && !id.startsWith(".") && !id.startsWith("/") && !id.startsWith("\0") && !id.startsWith("virtual:")) {
				const importPath = typeof __filename === "string" ? __filename : fileURLToPath(import.meta.url);
				const resolved = await this.resolve(id, importPath, { skipSelf: true });
				if (resolved) return resolved;
			}
		},
		load(id) {
			if (id === remoteEntryId) return parsePromise.then((_) => generateRemoteEntry(options, virtualExposesId, _command));
			if (id === virtualExposesId) return generateExposes(options);
			if (_command === "serve" && id.includes(getHostAutoInitPath())) return id;
		},
		transform(code, id) {
			return mapCodeToCodeWithSourcemap((() => {
				if (!filter(id)) return;
				if (id.includes(remoteEntryId)) return parsePromise.then((_) => generateRemoteEntry(options, virtualExposesId, _command));
				if (id === virtualExposesId) return generateExposes(options);
				if (id.includes(getHostAutoInitPath())) {
					if (_command === "serve") {
						const host = typeof viteConfig.server?.host === "string" && viteConfig.server.host !== "0.0.0.0" ? viteConfig.server.host : "localhost";
						const publicPath = JSON.stringify(resolvePublicPath(options, viteConfig.base) + options.filename);
						return `
          if (typeof window !== 'undefined') {
            const origin = (${!options.ignoreOrigin}) ? window.origin : "//${host}:${viteConfig.server?.port}"
            const remoteEntryPromise = await import(origin + ${publicPath})
            // __tla only serves as a hack for vite-plugin-top-level-await.
            Promise.resolve(remoteEntryPromise)
            .then(remoteEntry => {
              return Promise.resolve(remoteEntry.__tla)
                .then(remoteEntry.init).catch(remoteEntry.init)
            })
          }
          `;
					}
					return code;
				}
			})());
		}
	};
}
//#endregion
//#region src/plugins/pluginProxyRemotes.ts
createFilter();
function pluginProxyRemotes_default(options) {
	const { remotes } = options;
	return {
		name: "proxyRemotes",
		config(config, { command: _command }) {
			const isRolldown = !!this?.meta?.rolldownVersion;
			Object.keys(remotes).forEach((key) => {
				const remote = remotes[key];
				config.resolve.alias.push({
					find: new RegExp(`^(${remote.name}(\/.*|$))`),
					replacement: "$1",
					customResolver(source) {
						const remoteModule = getRemoteVirtualModule(source, _command, isRolldown);
						addUsedRemote(remote.name, source);
						return remoteModule.getPath();
					}
				});
			});
		}
	};
}
//#endregion
//#region src/utils/PromiseStore.ts
/**
* example:
* const store = new PromiseStore<number>();
* store.get("example").then((result) => {
*  console.log("Result from example:", result); // 42
* });
* setTimeout(() => {
*  store.set("example", Promise.resolve(42));
* }, 2000);
*/
var PromiseStore = class {
	constructor() {
		this.promiseMap = /* @__PURE__ */ new Map();
		this.resolveMap = /* @__PURE__ */ new Map();
	}
	set(id, promise) {
		if (this.resolveMap.has(id)) {
			promise.then(this.resolveMap.get(id));
			this.resolveMap.delete(id);
		}
		this.promiseMap.set(id, promise);
	}
	get(id) {
		if (this.promiseMap.has(id)) return this.promiseMap.get(id);
		const pendingPromise = new Promise((resolve) => {
			this.resolveMap.set(id, resolve);
		});
		this.promiseMap.set(id, pendingPromise);
		return pendingPromise;
	}
};
//#endregion
//#region src/plugins/pluginProxySharedModule_preBuild.ts
function proxySharedModule(options) {
	const { shared = {} } = options;
	let _config;
	let _command = "serve";
	let isVinext = false;
	const savePrebuild = new PromiseStore();
	return [{
		name: "generateLocalSharedImportMap",
		enforce: "post",
		load(id) {
			if (id.includes(getLocalSharedImportMapPath())) return parsePromise.then((_) => generateLocalSharedImportMap());
		},
		transform(_, id) {
			if (id.includes(getLocalSharedImportMapPath())) return mapCodeToCodeWithSourcemap(parsePromise.then((_) => generateLocalSharedImportMap()));
		}
	}, {
		name: "proxyPreBuildShared",
		enforce: "post",
		config(config, { command }) {
			setPackageDetectionCwd(config.root || process.cwd());
			isVinext = hasPackageDependency("vinext");
			const isRolldown = !!this?.meta?.rolldownVersion;
			_command = command;
			config.resolve.alias.push(...Object.keys(shared).filter((key) => !(isVinext && key === "react")).map((key) => {
				const keyBase = key.endsWith("/") ? key.slice(0, -1) : key;
				const escapeRegex = (value) => value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
				const escapedKeyBase = escapeRegex(keyBase);
				const pattern = key.endsWith("/") ? `^(${escapedKeyBase}(?:\\/.*)?)$` : `^(${escapedKeyBase})$`;
				return {
					find: new RegExp(pattern),
					replacement: "$1",
					customResolver(source, importer) {
						if (/\.css$/.test(source)) return;
						if (isVinext && source === "react") return;
						if (importer && importer.includes("localSharedImportMap")) return;
						if (key.endsWith("/") && source !== key.slice(0, -1)) return;
						const loadSharePath = getLoadShareModulePath(source, isRolldown, command);
						writeLoadShareModule(source, shared[key], command, isRolldown);
						writePreBuildLibPath(source);
						addUsedShares(source);
						writeLocalSharedImportMap();
						return this.resolve(loadSharePath, importer);
					}
				};
			}));
			config.resolve.alias.push(...Object.keys(shared).filter((key) => !(isVinext && key === "react")).map((key) => {
				return command === "build" ? {
					find: new RegExp(`(.*${PREBUILD_TAG}.*)`),
					replacement: function($1) {
						return assertModuleFound(PREBUILD_TAG, $1).name;
					}
				} : {
					find: new RegExp(`(.*${PREBUILD_TAG}.*)`),
					replacement: "$1",
					async customResolver(source, importer) {
						const pkgName = assertModuleFound(PREBUILD_TAG, source).name;
						const result = await this.resolve(pkgName, importer).then((item) => item.id);
						if (_config && !result.includes(_config.cacheDir)) savePrebuild.set(pkgName, Promise.resolve(result));
						return await this.resolve(await savePrebuild.get(pkgName), importer);
					}
				};
			}));
		},
		configResolved(config) {
			_config = config;
			const isRolldown = !!config.experimental?.rolldownDev;
			Object.keys(shared).forEach((key) => {
				if (key.endsWith("/")) return;
				if (isVinext && key === "react") {
					addUsedShares(key);
					return;
				}
				writeLoadShareModule(key, shared[key], _command, isRolldown);
				writePreBuildLibPath(key);
				addUsedShares(key);
			});
			writeLocalSharedImportMap();
		}
	}];
}
//#endregion
//#region src/plugins/pluginVarRemoteEntry.ts
const VarRemoteEntry = () => {
	const mfOptions = getNormalizeModuleFederationOptions();
	const { name, varFilename, filename } = mfOptions;
	let viteConfig;
	return [{
		name: "module-federation-var-remote-entry",
		apply: "serve",
		configResolved(config) {
			viteConfig = config;
		},
		configureServer(server) {
			server.middlewares.use((req, res, next) => {
				if (!varFilename) {
					next();
					return;
				}
				if (req.url?.replace(/\?.*/, "") === (viteConfig.base + varFilename).replace(/^\/?/, "/")) {
					res.setHeader("Content-Type", "text/javascript");
					res.setHeader("Access-Control-Allow-Origin", "*");
					console.log({ filename });
					res.end(generateVarRemoteEntry(filename));
				} else next();
			});
		}
	}, {
		name: "module-federation-var-remote-entry",
		enforce: "post",
		config(config, { command }) {
			if (!config.build) config.build = {};
		},
		configResolved(config) {
			viteConfig = config;
		},
		async generateBundle(options, bundle) {
			if (!varFilename) return;
			if (!isValidVarName(name)) viteConfig.logger.warn(`Provided remote name "${name}" is not valid for "var" remoteEntry type, thus it's placed in globalThis['${name}'].\nIt may cause problems, so you would better want to use valid var name (see https://www.w3schools.com/js/js_variables.asp).`);
			const remoteEntryFile = findRemoteEntryFile(mfOptions.filename, bundle);
			if (!remoteEntryFile) throw new Error(`Couldn't find a remoteEntry chunk file for ${mfOptions.filename}, can't generate varRemoteEntry file`);
			this.emitFile({
				type: "asset",
				fileName: varFilename,
				source: generateVarRemoteEntry(remoteEntryFile)
			});
		}
	}];
	function isValidVarName(name) {
		return /^[A-Za-z_$][A-Za-z0-9_$]*$/.test(name);
	}
	/**
	* Generates the final "var" remote entry file
	* @param remoteEntryFile - Path to esm remote entry file
	* @returns Complete "var" remoteEntry.js file source
	*/
	function generateVarRemoteEntry(remoteEntryFile) {
		const { name, varFilename } = getNormalizeModuleFederationOptions();
		const isValidName = isValidVarName(name);
		return `
  ${isValidName ? `var ${name};` : ""}
  ${isValidName ? name : `globalThis['${name}']`} = (function () {
    function getScriptUrl() {
      const currentScript = document.currentScript;
      if (!currentScript) {
        console.error("[VarRemoteEntry] ${varFilename} script should be called from sync <script> tag (document.currentScript is undefined)")
        return '/';
      }
      return document.currentScript.src.replace(/\\/[^/]*$/, '/');
    }

    const entry = getScriptUrl() + '${remoteEntryFile}';

    return {
      get: (...args) => import(entry).then(m => m.get(...args)),
      init: (...args) => import(entry).then(m => m.init(...args)),
    };
  })();
  `;
	}
};
//#endregion
//#region src/utils/aliasToArrayPlugin.ts
var aliasToArrayPlugin_default = {
	name: "alias-transform-plugin",
	config: (config, { command }) => {
		if (!config.resolve) config.resolve = {};
		if (!config.resolve.alias) config.resolve.alias = [];
		const { alias } = config.resolve;
		if (typeof alias === "object" && !Array.isArray(alias)) config.resolve.alias = Object.entries(alias).map(([find, replacement]) => ({
			find,
			replacement
		}));
	}
};
//#endregion
//#region src/utils/controlChunkSanitizer.ts
const FEDERATION_CONTROL_CHUNK_HINTS = [
	"hostInit",
	"virtualExposes",
	"localSharedImportMap"
];
function stripEmptyPreloadCalls(code) {
	const helperImportRegex = /import\s*\{\s*_\s*as\s*(\w+)\s*\}\s*from\s*["'][^"']+["']\s*;?/g;
	const helperAliases = [...code.matchAll(helperImportRegex)].map((match) => match[1]);
	let nextCode = code;
	for (const alias of helperAliases) {
		const marker = `${alias}(()=>`;
		let start = nextCode.indexOf(marker);
		while (start !== -1) {
			const exprStart = start + marker.length;
			let depth = 0;
			let cursor = exprStart;
			let replacementEnd = -1;
			while (cursor < nextCode.length) {
				const char = nextCode[cursor];
				if (char === "(") depth++;
				else if (char === ")") depth--;
				else if (depth === 0 && nextCode.startsWith(",[],import.meta.url)", cursor)) {
					replacementEnd = cursor;
					break;
				}
				cursor++;
			}
			if (replacementEnd === -1) break;
			const expression = nextCode.slice(exprStart, replacementEnd);
			nextCode = nextCode.slice(0, start) + expression + nextCode.slice(replacementEnd + 20);
			start = nextCode.indexOf(marker, start + expression.length);
		}
	}
	nextCode = nextCode.replace(/import\s*["'][^"']*__loadShare__[^"']*["']\s*;?/g, "");
	nextCode = nextCode.replace(helperImportRegex, (statement, local) => {
		return new RegExp(`\\b${local}\\s*\\(`).test(nextCode.replace(statement, "")) ? statement : "";
	});
	return nextCode;
}
function isFederationControlChunk(fileName, filename) {
	return fileName.includes(filename) || FEDERATION_CONTROL_CHUNK_HINTS.some((hint) => fileName.includes(hint));
}
function sanitizeFederationControlChunk(code, fileName, filename) {
	let nextCode = stripEmptyPreloadCalls(code);
	if (fileName.includes("localSharedImportMap")) {
		const remoteEntryImportRegex = new RegExp(`import\\s*["'][^"']*${filename.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}["']\\s*;?`, "g");
		nextCode = nextCode.replace(remoteEntryImportRegex, "");
	}
	return nextCode;
}
//#endregion
//#region src/utils/normalizeOptimizeDeps.ts
var normalizeOptimizeDeps_default = {
	name: "normalizeOptimizeDeps",
	config: (config, { command }) => {
		let { optimizeDeps } = config;
		if (!optimizeDeps) {
			config.optimizeDeps = {};
			optimizeDeps = config.optimizeDeps;
		}
		if (!optimizeDeps.include) optimizeDeps.include = [];
		if (!optimizeDeps.exclude) optimizeDeps.exclude = [];
		if (!optimizeDeps.needsInterop) optimizeDeps.needsInterop = [];
	}
};
//#endregion
//#region src/index.ts
/**
* Plugin that runs FIRST to create virtual module files in the config hook.
* This prevents 504 "Outdated Optimize Dep" errors by ensuring files exist
* before Vite's optimization phase.
*/
function createEarlyVirtualModulesPlugin(options) {
	const { shared, remotes, virtualModuleDir } = options;
	return {
		name: "vite:module-federation-early-init",
		enforce: "pre",
		config(config, { command: _command }) {
			const root = config.root || process.cwd();
			setPackageDetectionCwd(root);
			const isVinext = hasPackageDependency("vinext");
			initVirtualModuleInfrastructure(root, virtualModuleDir);
			VirtualModule.setRoot(root);
			VirtualModule.ensureVirtualPackageExists();
			initVirtualModules(_command, getRemoteEntryId(options));
			if (_command !== "serve") return;
			const isRolldown = !!this?.meta?.rolldownVersion;
			if (remotes && Object.keys(remotes).length > 0) for (const key of Object.keys(remotes)) addUsedRemote(key, key);
			if (shared && Object.keys(shared).length > 0) {
				config.optimizeDeps = config.optimizeDeps || {};
				config.optimizeDeps.include = config.optimizeDeps.include || [];
				config.optimizeDeps.include.push(virtualRuntimeInitStatus.getImportId());
				for (const key of Object.keys(shared)) {
					if (key.endsWith("/")) continue;
					const shareItem = shared[key];
					if (isVinext && key === "react") {
						addUsedShares(key);
						continue;
					}
					getLoadShareModulePath(key, isRolldown);
					writeLoadShareModule(key, shareItem, _command, isRolldown);
					writePreBuildLibPath(key);
					addUsedShares(key);
					config.optimizeDeps.include.push(getPreBuildLibImportId(key));
				}
				writeLocalSharedImportMap();
			}
		}
	};
}
function federation(mfUserOptions) {
	const options = normalizeModuleFederationOptions(mfUserOptions);
	const isVinext = hasPackageDependency("vinext");
	const { name, remotes, shared, filename, hostInitInjectLocation } = options;
	if (!name) throw new Error("name is required");
	const remoteEntryId = getRemoteEntryId(options);
	const virtualExposesId = getVirtualExposesId(options);
	let command;
	return [
		createEarlyVirtualModulesPlugin(options),
		...isVinext ? [{
			name: "module-federation-vinext-react-server-build-alias",
			apply: "build",
			enforce: "pre",
			resolveId(id) {
				const reactServerEntryMap = {
					"react/jsx-runtime": "react/cjs/react-jsx-runtime.production.js",
					"react/jsx-dev-runtime": "react/cjs/react-jsx-dev-runtime.production.js"
				};
				if (!(id in reactServerEntryMap)) return;
				const environmentName = this?.environment?.name;
				if (!environmentName || environmentName === "client") return;
				const target = reactServerEntryMap[id];
				const reactPackageJson = createRequire$1(new URL(`file://${process.cwd()}/package.json`)).resolve("react/package.json");
				return path.join(path.dirname(reactPackageJson), target.replace(/^react\//, ""));
			}
		}] : [],
		{
			name: "vite:module-federation-config",
			enforce: "pre",
			config(_config, env) {
				command = env.command;
			},
			configResolved(config) {
				VirtualModule.setRoot(config.root);
				VirtualModule.ensureVirtualPackageExists();
				initVirtualModules(command, remoteEntryId);
			}
		},
		aliasToArrayPlugin_default,
		checkAliasConflicts({ shared }),
		normalizeOptimizeDeps_default,
		...pluginDts(options),
		...addEntry({
			entryName: "remoteEntry",
			entryPath: remoteEntryId,
			fileName: filename
		}),
		...addEntry({
			entryName: "hostInit",
			entryPath: () => getHostAutoInitPath(),
			inject: hostInitInjectLocation
		}),
		...addEntry({
			entryName: "virtualExposes",
			entryPath: virtualExposesId
		}),
		pluginProxyRemoteEntry_default({
			options,
			remoteEntryId,
			virtualExposesId
		}),
		pluginProxyRemotes_default(options),
		...pluginModuleParseEnd_default((id) => {
			return id.includes(getHostAutoInitImportId()) || id.includes(remoteEntryId) || id.includes(virtualExposesId) || id.includes(getLocalSharedImportMapPath());
		}, {
			moduleParseTimeout: options.moduleParseTimeout,
			moduleParseIdleTimeout: options.moduleParseIdleTimeout,
			virtualExposesId
		}),
		...proxySharedModule({ shared }),
		{
			name: "module-federation-esm-shims",
			enforce: "pre",
			apply: "build",
			config(config) {
				const runtimeInitId = virtualRuntimeInitStatus.getImportId();
				config.build = config.build || {};
				if (config.build.modulePreload !== false) {
					const currentModulePreload = config.build.modulePreload && typeof config.build.modulePreload === "object" ? config.build.modulePreload : {};
					const existingResolveDependencies = currentModulePreload.resolveDependencies;
					config.build.modulePreload = {
						...currentModulePreload,
						resolveDependencies(filename, deps, context) {
							const resolvedDeps = existingResolveDependencies ? existingResolveDependencies(filename, deps, context) : deps;
							const hostFile = path.basename(context.hostId);
							return context.hostType === "js" && (hostFile === options.filename || hostFile.includes("hostInit") || hostFile.includes("virtualExposes") || hostFile.includes("localSharedImportMap")) ? [] : resolvedDeps;
						}
					};
				}
				const applyManualChunks = (output) => {
					const existingManualChunks = output.manualChunks;
					output.manualChunks = function(id) {
						if (id.includes(runtimeInitId)) return "runtimeInit";
						if (id.includes("__loadShare__")) {
							const match = id.match(/([^/\\]+__loadShare__[^/\\]+)/);
							return match ? match[1] : "loadShare";
						}
						if (typeof existingManualChunks === "function") return existingManualChunks.apply(this, arguments);
						if (existingManualChunks && typeof existingManualChunks === "object") {
							for (const [key, ids] of Object.entries(existingManualChunks)) if (Array.isArray(ids) && ids.some((v) => id.includes(v))) return key;
						}
					};
				};
				config.build.rollupOptions = config.build.rollupOptions || {};
				if (!Array.isArray(config.build.rollupOptions.output)) applyManualChunks(config.build.rollupOptions.output ||= {});
				const buildWithRolldown = config.build;
				buildWithRolldown.rolldownOptions = buildWithRolldown.rolldownOptions || {};
				if (!Array.isArray(buildWithRolldown.rolldownOptions.output)) applyManualChunks(buildWithRolldown.rolldownOptions.output ||= {});
			},
			load(id) {
				if (id.startsWith("\0")) return;
				if (id.includes("__loadShare__") || id.includes("__loadRemote__")) {
					let code = readFileSync(id, "utf-8");
					code = code.replace(/import\s+["'][^"']*__prebuild__[^"']*["']\s*;?/g, "");
					code = code.replace(/export\s+\*\s+from\s+["'][^"']*__prebuild__[^"']*["']\s*;?/g, "");
					/**
					* Shared/remote shims only have `export default exportModule`.
					*
					* We add a second named export (__moduleExports) that holds the full
					* module namespace and point syntheticNamedExports at it.  This lets
					* Rollup resolve named imports (e.g. `import { useState } from 'react'`)
					* from the namespace while still applying its normal default-export
					* interop — which is needed for libraries like @emotion/styled where
					* `import styled from '@emotion/styled'` must receive the .default
					* function, not the raw namespace object.
					*
					* Using 'default' as the syntheticNamedExports key would skip the
					* interop and break default imports.
					*
					* @see https://rollupjs.org/plugin-development/#synthetic-named-exports
					*/
					code = code.replace("export default exportModule", "export const __moduleExports = exportModule;\nexport default exportModule.__esModule ? exportModule.default : exportModule");
					return {
						code,
						syntheticNamedExports: "__moduleExports"
					};
				}
			},
			generateBundle(_, bundle) {
				for (const [fileName, chunk] of Object.entries(bundle)) {
					if (chunk.type !== "chunk") continue;
					if (!isFederationControlChunk(fileName, filename)) continue;
					chunk.code = sanitizeFederationControlChunk(chunk.code, fileName, filename);
				}
				for (const [fileName, chunk] of Object.entries(bundle)) {
					if (chunk.type !== "chunk") continue;
					if (fileName.includes("__loadShare__")) continue;
					let code = chunk.code;
					let m;
					const importedFromLoadShare = /* @__PURE__ */ new Set();
					const importRegex = /import\s*\{([^}]+)\}\s*from\s*["'][^"']*__loadShare__[^"']*["']/g;
					while ((m = importRegex.exec(code)) !== null) for (const spec of m[1].split(",")) {
						const parts = spec.trim().split(/\s+as\s+/);
						const local = (parts[1] || parts[0]).trim();
						if (local) importedFromLoadShare.add(local);
					}
					const allInits = [];
					for (const v of importedFromLoadShare) if (new RegExp("\\(" + v + "\\(\\)\\s*,\\s*\\w+\\(\\w+\\)\\)").test(code)) allInits.push(v);
					if (allInits.length === 0) continue;
					const awaits = allInits.map((v) => `await ${v}();`).join("");
					const lastFromRegex = /\bfrom\s*["'][^"']*["']\s*;?/g;
					let lastFromEnd = -1;
					while ((m = lastFromRegex.exec(code)) !== null) lastFromEnd = m.index + m[0].length;
					if (lastFromEnd !== -1) {
						chunk.code = code.slice(0, lastFromEnd) + awaits + code.slice(lastFromEnd);
						continue;
					}
					const exportIdx = code.search(/\bexport\s*[{d]/);
					if (exportIdx !== -1) {
						chunk.code = code.slice(0, exportIdx) + awaits + code.slice(exportIdx);
						continue;
					}
				}
				const proxyChunks = /* @__PURE__ */ new Map();
				for (const [fileName, chunk] of Object.entries(bundle)) {
					if (chunk.type !== "chunk") continue;
					if (fileName.includes("__loadShare__") && fileName.includes("commonjs-proxy")) proxyChunks.set(fileName, {
						code: chunk.code,
						fileName
					});
				}
				if (proxyChunks.size > 0) for (const [fileName, chunk] of Object.entries(bundle)) {
					if (chunk.type !== "chunk") continue;
					if (fileName.includes("__loadShare__")) continue;
					let code = chunk.code;
					let modified = false;
					const claimedLocals = /* @__PURE__ */ new Set();
					for (const [proxyFileName, proxyInfo] of proxyChunks) {
						const proxyBaseName = proxyFileName.replace(/^.*\//, "").replace(/\.js$/, "").replace(/-[A-Za-z0-9_-]+$/, "");
						const importMatch = new RegExp(`import\\s*\\{([^}]+)\\}\\s*from\\s*["']([^"']*${proxyBaseName.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}[^"']*)["']\\s*;?`).exec(code);
						if (!importMatch) continue;
						const fullImport = importMatch[0];
						const bindings = importMatch[1].split(",").map((s) => {
							const parts = s.trim().split(/\s+as\s+/);
							return {
								imported: parts[0].trim(),
								local: (parts[1] || parts[0]).trim()
							};
						});
						const proxyCode = proxyInfo.code;
						const exportMapMatch = proxyCode.match(/export\s*\{([^}]+)\}/);
						if (!exportMapMatch) continue;
						const exportMap = {};
						for (const entry of exportMapMatch[1].split(",")) {
							const parts = entry.trim().split(/\s+as\s+/);
							if (parts.length === 2) exportMap[parts[1].trim()] = parts[0].trim();
						}
						const inlineable = [];
						const nonInlineable = [];
						const pendingLocals = new Set(bindings.map((binding) => binding.local));
						for (const b of bindings) {
							pendingLocals.delete(b.local);
							const proxyLocal = exportMap[b.imported];
							if (!proxyLocal) {
								claimedLocals.add(b.local);
								nonInlineable.push(b);
								continue;
							}
							const funcRe = new RegExp(`function\\s+${proxyLocal}\\s*\\([^)]*\\)\\s*\\{`);
							if (funcRe.test(proxyCode)) {
								const funcStart = proxyCode.search(funcRe);
								let depth = 0;
								let funcEnd = funcStart;
								for (let i = proxyCode.indexOf("{", funcStart); i < proxyCode.length; i++) if (proxyCode[i] === "{") depth++;
								else if (proxyCode[i] === "}") {
									depth--;
									if (depth === 0) {
										funcEnd = i + 1;
										break;
									}
								}
								const renamedFunc = proxyCode.slice(funcStart, funcEnd).replace(new RegExp(`function\\s+${proxyLocal}\\s*\\(`), `function ${b.local}(`);
								inlineable.push({
									local: b.local,
									funcBody: renamedFunc
								});
								claimedLocals.add(b.local);
							} else {
								const unavailableLocals = new Set(claimedLocals);
								pendingLocals.forEach((local) => unavailableLocals.add(local));
								const resolvedBinding = resolveProxyAlias(b, proxyLocal, code, fullImport, unavailableLocals);
								claimedLocals.add(resolvedBinding.local);
								nonInlineable.push(resolvedBinding);
							}
						}
						const hasRenamedAlias = nonInlineable.some((b) => bindings.find((ob) => ob.imported === b.imported)?.local !== b.local);
						if (inlineable.length === 0 && !hasRenamedAlias) continue;
						let replacement = "";
						if (nonInlineable.length > 0) replacement = `import{${nonInlineable.map((b) => b.imported === b.local ? b.imported : `${b.imported} as ${b.local}`).join(",")}}from"${importMatch[2]}";`;
						replacement += inlineable.map((f) => f.funcBody).join("");
						code = code.replace(fullImport, () => replacement);
						modified = true;
					}
					if (modified) chunk.code = code;
				}
			}
		},
		{
			name: "module-federation-strip-empty-preload-helper",
			enforce: "post",
			apply: "build",
			renderChunk(code, chunk) {
				if (!isFederationControlChunk(chunk.fileName, filename)) return;
				const nextCode = sanitizeFederationControlChunk(code, chunk.fileName, filename);
				return nextCode === code ? null : {
					code: nextCode,
					map: null
				};
			},
			writeBundle(outputOptions, bundle) {
				if (!outputOptions.dir) return;
				for (const chunk of Object.values(bundle)) {
					if (chunk.type !== "chunk") continue;
					if (!isFederationControlChunk(chunk.fileName, filename)) continue;
					const outputPath = path.join(outputOptions.dir, chunk.fileName);
					writeFileSync(outputPath, sanitizeFederationControlChunk(readFileSync(outputPath, "utf-8"), chunk.fileName, filename));
				}
			}
		},
		{
			name: "module-federation-dev-await-shared-init",
			apply: "serve",
			enforce: "post",
			transform(code, id) {
				if (!id.includes(".vite/deps/")) return;
				const initPattern = /\b(init_\w+__loadShare__\w+)\b/g;
				const initFns = /* @__PURE__ */ new Set();
				let match;
				while ((match = initPattern.exec(code)) !== null) initFns.add(match[1]);
				if (initFns.size === 0) return;
				if (![...initFns].some((fn) => {
					return code.includes(`${fn}(),`) || code.includes(`${fn}()`);
				})) return;
				if (/await\s+init_\w+__loadShare__/.test(code)) return;
				if (code.includes("__esmMin")) return;
				const awaits = [...initFns].map((fn) => `await ${fn}();`).join("\n");
				const topLevelImportRe = /^import\s/gm;
				let lastImportIdx = -1;
				let importMatch;
				while ((importMatch = topLevelImportRe.exec(code)) !== null) lastImportIdx = importMatch.index;
				if (lastImportIdx === -1) return;
				const lineEnd = code.indexOf("\n", lastImportIdx);
				return code.slice(0, lineEnd + 1) + awaits + "\n" + code.slice(lineEnd + 1);
			}
		},
		PluginDevProxyModuleTopLevelAwait(),
		{
			name: "module-federation-vite",
			enforce: "post",
			_options: options,
			config(config, { command: _command }) {
				const isRolldown = !!this?.meta?.rolldownVersion;
				let implementation = options.implementation;
				if (isRolldown) implementation = implementation.replace(/\.cjs(\.js)?$/, ".js");
				config.resolve.alias.push({
					find: "@module-federation/runtime",
					replacement: implementation
				});
				config.build = defu(config.build || {}, { commonjsOptions: { strictRequires: "auto" } });
				const virtualDir = options.virtualModuleDir || "__mf__virtual";
				config.optimizeDeps ||= {};
				config.optimizeDeps.include ||= [];
				config.optimizeDeps.include.push("@module-federation/runtime");
				config.optimizeDeps.include.push(virtualDir);
				config.ssr ||= {};
				config.ssr.noExternal ||= [];
				if (Array.isArray(config.ssr.noExternal)) config.ssr.noExternal.push(virtualDir);
				options.runtimePlugins.forEach((p) => {
					const pluginPath = typeof p === "string" ? p : p[0];
					if (pluginPath && !pluginPath.startsWith(".") && !pluginPath.startsWith("/") && !pluginPath.startsWith("\0") && !pluginPath.startsWith("virtual:")) config.optimizeDeps.include.push(pluginPath);
				});
				if (isRolldown) config.build = defu(config.build || {}, { target: "esnext" });
				else {
					config.optimizeDeps.needsInterop ||= [];
					config.optimizeDeps.needsInterop.push(virtualDir);
					config.optimizeDeps.needsInterop.push(getLocalSharedImportMapPath());
				}
				const resolvedTarget = options.target ?? (config.build?.ssr ? "node" : "web");
				if (!config.define) config.define = {};
				if (!("ENV_TARGET" in config.define)) config.define["ENV_TARGET"] = JSON.stringify(resolvedTarget);
				if (options.target && "ENV_TARGET" in config.define && config.define["ENV_TARGET"] !== JSON.stringify(options.target)) console.warn(`[module-federation] ENV_TARGET define (${config.define["ENV_TARGET"]}) differs from target option ("${options.target}"). ENV_TARGET will not be overridden.`);
			}
		},
		...Manifest(),
		...VarRemoteEntry(),
		...Object.keys(options.exposes).length > 0 ? [{
			name: "module-federation-fix-preload",
			enforce: "post",
			apply: "build",
			generateBundle(_, bundle) {
				for (const chunk of Object.values(bundle)) {
					if (chunk.type !== "chunk") continue;
					if (!chunk.code.includes("modulepreload")) continue;
					const replacement = "=function($1){return new URL(\"../\"+$1,import.meta.url).href}";
					const replaced = chunk.code.replace(/=\(?(\w+)(?:,\w+)?\)?\s*=>\s*["'`][^"'`]*["'`]\s*\+\s*\1/, replacement);
					if (replaced !== chunk.code) {
						chunk.code = replaced;
						continue;
					}
					chunk.code = chunk.code.replace(/=function\((\w+)(?:,\w+)?\)\{return\s*["'`][^"'`]*["'`]\s*\+\s*\1\s*\}/, replacement);
				}
			}
		}] : []
	];
}
//#endregion
export { federation };
