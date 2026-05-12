/**
 * slashcmdValidation.ts — frontend mirror of core/slashcmd/Validate.
 *
 * Produces the same error messages as the Go-side validator so inline
 * UI feedback matches server-side rejection. Call validateUserCommand
 * on every keystroke; only submit when it returns null.
 *
 * user-slash-commands-01KQ8TD9 WP07.
 */

import type { UserCommand, UserCommandInput } from './types';

const NAME_RE = /^[a-z][a-z0-9_-]{0,30}$/;
const VALID_KINDS = new Set(['text', 'tool', 'prompt']);
const VALID_INPUT_KINDS = new Set([
  'text',
  'enum',
  'number',
  'file',
  'artifact_ref',
  'project_ref',
]);

export interface ValidationError {
  field: string;
  message: string;
}

/**
 * validateUserCommand — runs all checks the server runs.
 *
 * Returns null when the command is valid, or the first ValidationError
 * found. The UI renders this under the relevant field.
 */
export function validateUserCommand(cmd: Partial<UserCommand>): ValidationError | null {
  // name
  if (!cmd.name || !NAME_RE.test(cmd.name)) {
    return {
      field: 'name',
      message:
        'Name must start with a lowercase letter and contain only lowercase letters, digits, hyphens, or underscores (max 31 chars).',
    };
  }

  // scope
  if (cmd.scope !== 'global' && cmd.scope !== 'project') {
    return { field: 'scope', message: 'Scope must be "global" or "project".' };
  }
  if (cmd.scope === 'project' && !cmd.projectId) {
    return {
      field: 'projectId',
      message: 'Project ID is required when scope is "project".',
    };
  }
  if (cmd.scope === 'global' && cmd.projectId) {
    return {
      field: 'projectId',
      message: 'Project ID must be empty when scope is "global".',
    };
  }

  // kind
  if (!cmd.kind || !VALID_KINDS.has(cmd.kind)) {
    return {
      field: 'kind',
      message: 'Kind must be one of "text", "tool", or "prompt".',
    };
  }

  // kind-specific
  if (cmd.kind === 'tool') {
    if (!cmd.tool) {
      return { field: 'tool', message: 'Tool name is required for kind "tool".' };
    }
    if (!cmd.toolArgsTemplate) {
      return {
        field: 'toolArgsTemplate',
        message: 'Tool args template is required for kind "tool".',
      };
    }
  } else {
    // text or prompt
    if (!cmd.body || cmd.body.trim() === '') {
      return {
        field: 'body',
        message: `Body is required for kind "${cmd.kind}".`,
      };
    }
  }

  // inputs
  if (cmd.inputs) {
    for (const inp of cmd.inputs) {
      const inputErr = validateInput(inp);
      if (inputErr) return inputErr;
    }
  }

  return null;
}

function validateInput(inp: UserCommandInput): ValidationError | null {
  if (!inp.name || inp.name.trim() === '') {
    return { field: 'inputs', message: 'Each input must have a non-empty name.' };
  }
  if (!VALID_INPUT_KINDS.has(inp.kind)) {
    return {
      field: 'inputs',
      message: `Input "${inp.name}": unknown kind "${inp.kind}".`,
    };
  }
  if (inp.kind === 'enum') {
    if (!inp.enumValues || inp.enumValues.length === 0) {
      return {
        field: 'inputs',
        message: `Input "${inp.name}": enum inputs must have at least one enum_value.`,
      };
    }
    if (inp.default !== undefined && inp.default !== '' && !inp.enumValues.includes(inp.default)) {
      return {
        field: 'inputs',
        message: `Input "${inp.name}": default value "${inp.default}" is not in enum_values.`,
      };
    }
  }
  return null;
}

/**
 * allValidationErrors — returns all validation errors (for a summary
 * panel that wants to show every problem at once, not just the first).
 */
export function allValidationErrors(cmd: Partial<UserCommand>): ValidationError[] {
  const errors: ValidationError[] = [];

  if (!cmd.name || !NAME_RE.test(cmd.name)) {
    errors.push({
      field: 'name',
      message:
        'Name must start with a lowercase letter and contain only lowercase letters, digits, hyphens, or underscores (max 31 chars).',
    });
  }
  if (cmd.scope !== 'global' && cmd.scope !== 'project') {
    errors.push({ field: 'scope', message: 'Scope must be "global" or "project".' });
  }
  if (cmd.scope === 'project' && !cmd.projectId) {
    errors.push({
      field: 'projectId',
      message: 'Project ID is required when scope is "project".',
    });
  }
  if (cmd.scope === 'global' && cmd.projectId) {
    errors.push({
      field: 'projectId',
      message: 'Project ID must be empty when scope is "global".',
    });
  }
  if (!cmd.kind || !VALID_KINDS.has(cmd.kind)) {
    errors.push({
      field: 'kind',
      message: 'Kind must be one of "text", "tool", or "prompt".',
    });
  }
  if (cmd.kind === 'tool') {
    if (!cmd.tool) {
      errors.push({ field: 'tool', message: 'Tool name is required for kind "tool".' });
    }
    if (!cmd.toolArgsTemplate) {
      errors.push({
        field: 'toolArgsTemplate',
        message: 'Tool args template is required for kind "tool".',
      });
    }
  } else if (cmd.kind === 'text' || cmd.kind === 'prompt') {
    if (!cmd.body || cmd.body.trim() === '') {
      errors.push({
        field: 'body',
        message: `Body is required for kind "${cmd.kind}".`,
      });
    }
  }
  if (cmd.inputs) {
    for (const inp of cmd.inputs) {
      const err = validateInput(inp);
      if (err) errors.push(err);
    }
  }

  return errors;
}
