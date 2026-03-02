/** @type {import('tailwindcss').Config} */
export default {
  content: ['./src/**/*.{html,js,svelte,ts}'],
  theme: {
    extend: {
      colors: {
        md: {
          background: '#FFFBFE',
          'on-background': '#1C1B1F',
          primary: '#6750A4',
          'on-primary': '#FFFFFF',
          'primary-container': '#EADDFF',
          'on-primary-container': '#21005D',
          secondary: '#625B71',
          'on-secondary': '#FFFFFF',
          'secondary-container': '#E8DEF8',
          'on-secondary-container': '#1D192B',
          tertiary: '#7D5260',
          'on-tertiary': '#FFFFFF',
          'surface-container': '#F3EDF7',
          'surface-container-low': '#E7E0EC',
          'surface-variant': '#E7E0EC',
          'on-surface-variant': '#49454F',
          outline: '#79747E',
          'outline-variant': '#CAC4D0',
        },
      },
      fontFamily: {
        sans: ['Roboto', 'sans-serif'],
      },
      borderRadius: {
        'md-sm': '12px',
        'md-base': '16px',
        'md-large': '24px',
        'md-xl': '32px',
        'md-2xl': '48px',
      },
      boxShadow: {
        'md-sm': '0 1px 2px 0 rgba(0, 0, 0, 0.05)',
        'md-base': '0 4px 6px -1px rgba(0, 0, 0, 0.1), 0 2px 4px -1px rgba(0, 0, 0, 0.06)',
        'md-lg': '0 10px 15px -3px rgba(0, 0, 0, 0.1), 0 4px 6px -2px rgba(0, 0, 0, 0.05)',
      },
      transitionTimingFunction: {
        'md-emphasized': 'cubic-bezier(0.2, 0, 0, 1)',
      }
    },
  },
  plugins: [],
}

