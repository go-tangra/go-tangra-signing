import type { ShellContext, TangraModule } from './types';

export function registerModule(ctx: ShellContext, module: TangraModule) {
  // Capture the catch-all 404 route so we can re-add it at the end
  const fallbackRoute = ctx.router.getRoutes().find(
    (r) => r.name === 'FallbackNotFound' || r.path.includes(':path') || r.path.includes(':pathMatch'),
  );

  for (const route of module.routes) {
    const pathsToRemove = new Set<string>();
    pathsToRemove.add(route.path);
    if (route.children) {
      for (const child of route.children) {
        const childPath = child.path.startsWith('/')
          ? child.path
          : `${route.path}/${child.path}`;
        pathsToRemove.add(childPath);
      }
    }

    for (const existing of ctx.router.getRoutes()) {
      if (pathsToRemove.has(existing.path) && existing.name) {
        ctx.router.removeRoute(existing.name);
      }
    }

    ctx.router.addRoute(route);
  }

  // Move the catch-all 404 route to the end so module routes take precedence
  if (fallbackRoute?.name) {
    ctx.router.removeRoute(fallbackRoute.name);
    ctx.router.addRoute(fallbackRoute as any);
  }

  for (const [lang, messages] of Object.entries(module.locales)) {
    ctx.i18n.global.mergeLocaleMessage(lang, { [module.id]: messages });
  }
}
