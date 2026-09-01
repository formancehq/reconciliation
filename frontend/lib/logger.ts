/**
 * Centralized logging utility with level-based filtering.
 * Usage: const log = createLogger('ModuleName');
 *        log.info('message', optionalData);
 *
 * Set log level at runtime: localStorage.setItem('log_level', 'debug')
 * Levels: debug < info < warn < error
 */

export type LogLevel = 'debug' | 'info' | 'warn' | 'error';

const LEVEL_ORDER: Record<LogLevel, number> = { debug: 0, info: 1, warn: 2, error: 3 };

const getMinLevel = (): LogLevel => {
  if (typeof window === 'undefined') return 'info';
  try {
    return (window.localStorage.getItem('log_level') as LogLevel) || 'info';
  } catch {
    return 'info';
  }
};

const shouldLog = (level: LogLevel): boolean =>
  LEVEL_ORDER[level] >= LEVEL_ORDER[getMinLevel()];

export interface Logger {
  debug: (msg: string, ...args: unknown[]) => void;
  info: (msg: string, ...args: unknown[]) => void;
  warn: (msg: string, ...args: unknown[]) => void;
  error: (msg: string, ...args: unknown[]) => void;
}

const createLogger = (module: string): Logger => {
  const prefix = `[${module}]`;
  return {
    debug: (msg, ...args) => { if (shouldLog('debug')) console.debug(prefix, msg, ...args); },
    info: (msg, ...args) => { if (shouldLog('info')) console.log(prefix, msg, ...args); },
    warn: (msg, ...args) => { if (shouldLog('warn')) console.warn(prefix, msg, ...args); },
    error: (msg, ...args) => { if (shouldLog('error')) console.error(prefix, msg, ...args); },
  };
};

export default createLogger;
