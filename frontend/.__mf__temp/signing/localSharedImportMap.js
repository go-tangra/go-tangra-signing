
// Windows temporarily needs this file, https://github.com/module-federation/vite/issues/68

    import {loadShare} from "@module-federation/runtime";
    const importMap = {
      
        "ant-design-vue": async () => {
          let pkg = await import("__mf__virtual/signing__prebuild__ant_mf_2_design_mf_2_vue__prebuild__.js");
            return pkg;
        }
      ,
        "pinia": async () => {
          let pkg = await import("__mf__virtual/signing__prebuild__pinia__prebuild__.js");
            return pkg;
        }
      ,
        "vue": async () => {
          let pkg = await import("__mf__virtual/signing__prebuild__vue__prebuild__.js");
            return pkg;
        }
      ,
        "vue-router": async () => {
          let pkg = await import("__mf__virtual/signing__prebuild__vue_mf_2_router__prebuild__.js");
            return pkg;
        }
      
    }
      const usedShared = {
      
          "ant-design-vue": {
            name: "ant-design-vue",
            version: "4.2.6",
            scope: ["default"],
            loaded: false,
            from: "signing",
            async get () {
              if (false) {
                throw new Error(`Shared module '${"ant-design-vue"}' must be provided by host`);
              }
              usedShared["ant-design-vue"].loaded = true
              const {"ant-design-vue": pkgDynamicImport} = importMap
              const res = await pkgDynamicImport()
              const exportModule = false && "ant-design-vue" === "react"
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
              singleton: true,
              requiredVersion: "^4.2.6",
              
            }
          }
        ,
          "pinia": {
            name: "pinia",
            version: "2.2.2",
            scope: ["default"],
            loaded: false,
            from: "signing",
            async get () {
              if (false) {
                throw new Error(`Shared module '${"pinia"}' must be provided by host`);
              }
              usedShared["pinia"].loaded = true
              const {"pinia": pkgDynamicImport} = importMap
              const res = await pkgDynamicImport()
              const exportModule = false && "pinia" === "react"
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
              singleton: true,
              requiredVersion: "^2.2.2",
              
            }
          }
        ,
          "vue": {
            name: "vue",
            version: "3.5.13",
            scope: ["default"],
            loaded: false,
            from: "signing",
            async get () {
              if (false) {
                throw new Error(`Shared module '${"vue"}' must be provided by host`);
              }
              usedShared["vue"].loaded = true
              const {"vue": pkgDynamicImport} = importMap
              const res = await pkgDynamicImport()
              const exportModule = false && "vue" === "react"
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
              singleton: true,
              requiredVersion: "^3.5.13",
              
            }
          }
        ,
          "vue-router": {
            name: "vue-router",
            version: "4.5.0",
            scope: ["default"],
            loaded: false,
            from: "signing",
            async get () {
              if (false) {
                throw new Error(`Shared module '${"vue-router"}' must be provided by host`);
              }
              usedShared["vue-router"].loaded = true
              const {"vue-router": pkgDynamicImport} = importMap
              const res = await pkgDynamicImport()
              const exportModule = false && "vue-router" === "react"
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
              singleton: true,
              requiredVersion: "^4.5.0",
              
            }
          }
        
    }
      const usedRemotes = [
                {
                  entryGlobalName: "shell",
                  name: "shell",
                  type: "module",
                  entry: "/remoteEntry.js",
                  shareScope: "default",
                }
          
      ]
      export {
        usedShared,
        usedRemotes
      }
      