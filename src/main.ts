// first: takes the secret out of the address's fragment before the router reads the fragment as a route
import './lib/keyBoot.ts'
import './app.css'
import { mount } from 'svelte'
import App from './App.svelte'
import { initTheme } from './lib/theme/theme.svelte.ts'
import { LOOK_CSS } from './lib/theme/lookCss.ts'

// the look's rules go first in the head: app.css and a theme's own CSS come after them, so a theme's rule wins a tie
const look = document.createElement('style')
look.id = 'conn-look'
look.textContent = LOOK_CSS
document.head.prepend(look)

initTheme()

mount(App, { target: document.getElementById('app')! })
