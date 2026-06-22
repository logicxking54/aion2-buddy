/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{vue,js,ts,jsx,tsx}'],
  theme: {
    extend: {
      colors: {
        // Aion 2 Buddy palette — dark game-overlay look with a cyan/gold accent
        ink: {
          900: '#0b0f17',
          800: '#0f1623',
          700: '#152033',
          600: '#1c2a42',
          500: '#27384f',
        },
        accent: {
          DEFAULT: '#38d6c4',
          soft: '#7ef0e3',
          gold: '#e7b96b',
        },
      },
      fontFamily: {
        sans: ['Nunito', 'Segoe UI', 'system-ui', 'sans-serif'],
      },
    },
  },
  plugins: [],
}
