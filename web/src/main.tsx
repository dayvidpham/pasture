import React from 'react'
import ReactDOM from 'react-dom/client'
import { ToastProvider } from '@peasant-labs/fairtrade/ui'

import '@peasant-labs/fairtrade/fonts.css'
import '@peasant-labs/fairtrade/tokens.css'
import '@peasant-labs/fairtrade/base.css'
import '@peasant-labs/fairtrade/components.css'
import './app.css'

import App from './App'

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <ToastProvider placement="bottom-right">
      <App />
    </ToastProvider>
  </React.StrictMode>,
)
