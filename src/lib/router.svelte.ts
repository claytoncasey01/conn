import { noteNavigation } from './fx.ts'
import { parseRoute } from './routes.ts'

export { href, type Route } from './routes.ts'

export const router = $state({ route: parseRoute(location.hash) })
addEventListener('hashchange', () => {
  noteNavigation()
  router.route = parseRoute(location.hash)
  scrollTo(0, 0)
})
