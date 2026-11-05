import { useEffect, useState } from "react";
import { Service as AppInfo } from "../bindings/gpumon/internal/appinfo";

// App 為主視窗的頁面, 目前只顯示應用程式名稱與完整版本字串.
export default function App() {
  const [name, setName] = useState("");
  const [version, setVersion] = useState("");

  useEffect(() => {
    AppInfo.Name().then(setName);
    AppInfo.Version().then(setVersion);
  }, []);

  return (
    <main className="app">
      <h1>{name}</h1>
      <p>{version}</p>
    </main>
  );
}
