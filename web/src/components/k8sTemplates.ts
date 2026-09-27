/** Шаблоны объектов Kubernetes: «Новый объект» и «+ объект» блочного режима. */
export const K8S_TEMPLATES: Record<string, (name: string, ns: string) => string> = {
  deployment: (name, ns) => `apiVersion: apps/v1
kind: Deployment
metadata:
  name: ${name}
  namespace: ${ns}
  labels:
    app: ${name}
spec:
  replicas: 2
  selector:
    matchLabels:
      app: ${name}
  template:
    metadata:
      labels:
        app: ${name}
    spec:
      containers:
        - name: ${name}
          image: nginx:1.27
          ports:
            - containerPort: 80
          resources:
            requests:
              cpu: 50m
              memory: 64Mi
            limits:
              memory: 256Mi
---
apiVersion: v1
kind: Service
metadata:
  name: ${name}
  namespace: ${ns}
spec:
  selector:
    app: ${name}
  ports:
    - port: 80
      targetPort: 80
`,
  ingress: (name, ns) => `apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: ${name}
  namespace: ${ns}
spec:
  rules:
    - host: ${name}.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: ${name}
                port:
                  number: 80
`,
  configmap: (name, ns) => `apiVersion: v1
kind: ConfigMap
metadata:
  name: ${name}
  namespace: ${ns}
data:
  key: value
`,
  cronjob: (name, ns) => `apiVersion: batch/v1
kind: CronJob
metadata:
  name: ${name}
  namespace: ${ns}
spec:
  schedule: "0 3 * * *"
  concurrencyPolicy: Forbid
  jobTemplate:
    spec:
      template:
        spec:
          restartPolicy: OnFailure
          containers:
            - name: ${name}
              image: busybox:1.36
              command: ["sh", "-c", "date; echo hello"]
`,
  empty: () => '',
}

/** Заготовки элементов списков блочного режима (отступ — у списка). */
export const K8S_ITEM_SNIPPETS: Record<string, string> = {
  container: `- name: app
  image: nginx:1.27
  ports:
    - containerPort: 80
  resources:
    requests:
      cpu: 50m
      memory: 64Mi
    limits:
      memory: 256Mi
`,
  volume: `- name: data
  emptyDir: {}
`,
  port: `- name: http
  port: 80
  targetPort: 80
`,
  rule: `- host: app.example.com
  http:
    paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: app
            port:
              number: 80
`,
  key: `key: value
`,
}
