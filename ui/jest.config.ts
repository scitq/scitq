module.exports = {
    transform: {
      '^.+\\.svelte$': 'svelte-jester',
      '^.+\\.ts$': 'ts-jest',
    },
    moduleFileExtensions: ['js', 'ts', 'svelte'],
    setupFilesAfterEnv: ['@testing-library/jest-dom/extend-expect'],
    testEnvironment: 'jsdom',
    testMatch: ['**/*.test.ts', '**/*.test.tsx', '**/*.test.svelte'],
  };