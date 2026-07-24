using System;
using System.Collections.Generic;
using System.Globalization;
using System.Text;

namespace GolemEngine.Unity.Editor
{
    /// <summary>Deterministic minimal YAML emitter for Golem Scribe artifacts.</summary>
    public static class GolemYamlWriter
    {
        /// <summary>Escapes a YAML double-quoted scalar when needed; plain scalars are preferred.</summary>
        public static string FormatScalar(string value)
        {
            if (value == null)
            {
                return "null";
            }

            if (value.Length == 0)
            {
                return "\"\"";
            }

            if (NeedsQuotes(value))
            {
                return "\"" + EscapeDoubleQuoted(value) + "\"";
            }

            return value;
        }

        /// <summary>Formats an integer scalar.</summary>
        public static string FormatInt(int value)
        {
            return value.ToString(CultureInfo.InvariantCulture);
        }

        /// <summary>Formats an unsigned integer scalar.</summary>
        public static string FormatUInt(uint value)
        {
            return value.ToString(CultureInfo.InvariantCulture);
        }

        /// <summary>Formats a 64-bit integer scalar.</summary>
        public static string FormatLong(long value)
        {
            return value.ToString(CultureInfo.InvariantCulture);
        }

        /// <summary>Formats an unsigned 64-bit integer scalar.</summary>
        public static string FormatULong(ulong value)
        {
            return value.ToString(CultureInfo.InvariantCulture);
        }

        /// <summary>Formats a float scalar with invariant culture.</summary>
        public static string FormatFloat(float value)
        {
            return value.ToString("G9", CultureInfo.InvariantCulture);
        }

        /// <summary>Formats a double scalar with invariant culture.</summary>
        public static string FormatDouble(double value)
        {
            return value.ToString("G17", CultureInfo.InvariantCulture);
        }

        /// <summary>Formats a boolean scalar as lowercase YAML true/false.</summary>
        public static string FormatBool(bool value)
        {
            return value ? "true" : "false";
        }

        /// <summary>Builds a document that starts with the Scribe marker line.</summary>
        public static string BuildDocument(IEnumerable<string> bodyLines)
        {
            var builder = new StringBuilder();
            builder.Append(GolemScribeConstants.MarkerLine);
            builder.Append('\n');
            foreach (var line in bodyLines)
            {
                builder.Append(line);
                builder.Append('\n');
            }
            return builder.ToString();
        }

        /// <summary>Returns true when existing file text is owned by Scribe.</summary>
        public static bool IsScribeOwned(string fileText)
        {
            if (string.IsNullOrEmpty(fileText))
            {
                return false;
            }

            using (var reader = new System.IO.StringReader(fileText))
            {
                string line;
                while ((line = reader.ReadLine()) != null)
                {
                    if (line.Length == 0)
                    {
                        continue;
                    }
                    return line.TrimEnd() == GolemScribeConstants.MarkerLine;
                }
            }
            return false;
        }

        /// <summary>
        /// Parses a YAML scalar token, supporting double-quoted escapes and stripping trailing inline comments.
        /// </summary>
        public static bool TryParseScalarToken(string raw, out string value)
        {
            value = null;
            if (raw == null)
            {
                return false;
            }

            raw = raw.Trim();
            if (raw.Length == 0)
            {
                return false;
            }

            if (raw[0] == '"')
            {
                var builder = new StringBuilder(raw.Length);
                for (var i = 1; i < raw.Length; i++)
                {
                    var c = raw[i];
                    if (c == '\\')
                    {
                        if (i + 1 >= raw.Length)
                        {
                            return false;
                        }

                        var next = raw[++i];
                        switch (next)
                        {
                            case 'n':
                                builder.Append('\n');
                                break;
                            case 'r':
                                builder.Append('\r');
                                break;
                            case 't':
                                builder.Append('\t');
                                break;
                            case 'x':
                                if (i + 2 >= raw.Length)
                                {
                                    return false;
                                }

                                var hex = raw.Substring(i + 1, 2);
                                if (!byte.TryParse(hex, NumberStyles.AllowHexSpecifier, CultureInfo.InvariantCulture, out var b))
                                {
                                    return false;
                                }

                                builder.Append((char)b);
                                i += 2;
                                break;
                            default:
                                builder.Append(next);
                                break;
                        }
                        continue;
                    }

                    if (c == '"')
                    {
                        value = builder.ToString();
                        return true;
                    }

                    builder.Append(c);
                }
                return false;
            }

            if (raw[0] == '\'')
            {
                var end = raw.IndexOf('\'', 1);
                if (end < 0)
                {
                    return false;
                }
                value = raw.Substring(1, end - 1);
                return true;
            }

            var hash = IndexOfUnquotedComment(raw);
            if (hash >= 0)
            {
                raw = raw.Substring(0, hash).TrimEnd();
            }

            value = raw;
            return value.Length > 0;
        }

        private static string EscapeDoubleQuoted(string value)
        {
            var builder = new StringBuilder(value.Length + 8);
            foreach (var c in value)
            {
                switch (c)
                {
                    case '\\':
                        builder.Append("\\\\");
                        break;
                    case '"':
                        builder.Append("\\\"");
                        break;
                    case '\n':
                        builder.Append("\\n");
                        break;
                    case '\r':
                        builder.Append("\\r");
                        break;
                    case '\t':
                        builder.Append("\\t");
                        break;
                    default:
                        if (c < 0x20)
                        {
                            builder.Append("\\x");
                            builder.Append(((int)c).ToString("x2", CultureInfo.InvariantCulture));
                        }
                        else
                        {
                            builder.Append(c);
                        }
                        break;
                }
            }
            return builder.ToString();
        }

        /// <summary>
        /// Finds a YAML comment marker (<c>#</c>) that is outside quotes and either at the start
        /// or preceded by whitespace (YAML requires whitespace before <c>#</c> comments).
        /// </summary>
        private static int IndexOfUnquotedComment(string raw)
        {
            var inDouble = false;
            var inSingle = false;
            for (var i = 0; i < raw.Length; i++)
            {
                var c = raw[i];
                if (inDouble)
                {
                    if (c == '\\' && i + 1 < raw.Length)
                    {
                        i++;
                        continue;
                    }
                    if (c == '"')
                    {
                        inDouble = false;
                    }
                    continue;
                }

                if (inSingle)
                {
                    if (c == '\'')
                    {
                        inSingle = false;
                    }
                    continue;
                }

                if (c == '"')
                {
                    inDouble = true;
                    continue;
                }

                if (c == '\'')
                {
                    inSingle = true;
                    continue;
                }

                if (c == '#' && (i == 0 || char.IsWhiteSpace(raw[i - 1])))
                {
                    return i;
                }
            }
            return -1;
        }

        private static bool NeedsQuotes(string value)
        {
            if (value == "true" || value == "false" || value == "null" || value == "yes" || value == "no")
            {
                return true;
            }

            if (double.TryParse(value, NumberStyles.Float, CultureInfo.InvariantCulture, out _))
            {
                return true;
            }

            foreach (var c in value)
            {
                // Quote whitespace (including space/newline/tab) and C0 controls.
                if (c <= 0x20)
                {
                    return true;
                }

                if (c == ':' || c == '#' || c == '{' || c == '}' || c == '[' || c == ']' ||
                    c == ',' || c == '&' || c == '*' || c == '!' || c == '|' || c == '>' ||
                    c == '\'' || c == '"' || c == '%' || c == '@' || c == '`')
                {
                    return true;
                }
            }
            return false;
        }
    }
}
