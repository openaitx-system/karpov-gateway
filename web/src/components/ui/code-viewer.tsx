"use client";

import * as React from "react";
import Editor from "react-simple-code-editor";
import Prism from "prismjs";
import "prismjs/components/prism-json";
import "prismjs/components/prism-bash";
import "prismjs/themes/prism-tomorrow.css";

interface CodeViewerProps {
  value?: string;
  code?: string;
  language?: "json" | "bash";
  className?: string;
  style?: React.CSSProperties;
}

export function CodeViewer({ value, code, language = "json", className, style }: CodeViewerProps) {
  const content = value ?? code ?? "";
  const grammar = (language === "bash" ? Prism.languages.bash : Prism.languages.json) ?? Prism.languages.json;
  const langName = language === "bash" ? "bash" : "json";

  return (
    <div className={className} style={style}>
      <Editor
        value={content}
        onValueChange={() => {}}
        highlight={(c) => grammar ? Prism.highlight(c, grammar, langName) : c}
        padding={16}
        readOnly
        style={{
          fontFamily: "ui-monospace, SFMono-Regular, 'SF Mono', Menlo, Consolas, monospace",
          fontSize: 13,
          lineHeight: 1.6,
          minHeight: "100%",
          background: "transparent",
          overflow: "auto",
        }}
        textareaClassName="focus:outline-none"
      />
    </div>
  );
}
