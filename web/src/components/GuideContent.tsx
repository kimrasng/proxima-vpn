import "./guides.css";

type Props = {
  html: string;
};

export default function GuideContent({ html }: Props) {
  // `html` is produced by the server's RenderGuide (goldmark + bluemonday
  // allow-list); nothing authored in the browser reaches this element unsanitized.
  return <div className="guide-content" dangerouslySetInnerHTML={{ __html: html }} />;
}
